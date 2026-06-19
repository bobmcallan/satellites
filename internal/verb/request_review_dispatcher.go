// Gate-skill dispatcher seam for the reviewer gate (satellites story review).
//
// The verb composes a gate run as: read story → resolve workflow skill →
// pick transition → mint reviewer key → invoke gate skill → parse decision.
// The invocation step is the only piece that talks to the outside world
// (a `claude -p` subprocess in prod, a stub in tests), so it lives behind
// this interface. Tests inject a fake; the server boot wires the prod
// `claude -p` dispatcher.

package verb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/frontmatter"
	"github.com/bobmcallan/satellites/internal/ledger"
)

// GateInput is what the dispatcher receives. The verb hands over the
// minimum a gate skill needs to make its call: which skill to dispatch and
// the story body + recent ledger to feed it on stdin.
//
// ProjectID/WorkspaceID ride along so a skill that enacts its own
// transition (sty_db5cdef0) can stamp the spine rows it writes
// (review_accept/review_reject/status_transition) with the same
// correlation the client used to, keeping the portal /ledger view intact.
type GateInput struct {
	SkillName    string
	StoryID      string
	ProjectID    string
	WorkspaceID  string
	StoryBody    string
	StoryStatus  string
	RecentLedger []ledger.Entry
	WorktreeRoot string
	Timeout      time.Duration
}

// GateOutput is what the dispatcher returns — the gate's verdict. The gate
// derives its own target status from the story's `## Workflow` and enacts it
// itself, so the dispatcher no longer carries a next_status back.
type GateOutput struct {
	Decision string `json:"decision"`
	Notes    string `json:"notes,omitempty"`
}

// GateDispatcher is the indirection seam. One method, one call — keeps
// the contract narrow so test fakes are obvious and the prod impl stays
// small.
type GateDispatcher interface {
	Dispatch(ctx context.Context, in GateInput) (GateOutput, error)
}

// GateDispatcherFunc adapts an ordinary function to the GateDispatcher
// interface — handy for test injection.
type GateDispatcherFunc func(ctx context.Context, in GateInput) (GateOutput, error)

// Dispatch satisfies GateDispatcher.
func (f GateDispatcherFunc) Dispatch(ctx context.Context, in GateInput) (GateOutput, error) {
	return f(ctx, in)
}

// ClaudeCLIGateDispatcher invokes `claude -p --append-system-prompt
// <gate-body>` as a subprocess in WorktreeRoot. The gate skill's body is
// read from the worktree (.claude/skills/<name>/SKILL.md) and delivered
// as the appended system prompt — the claude CLI has no `--skill` flag
// (sty_1312d692). The story body + recent ledger arrive on stdin as the
// prompt; the subprocess inherits the operator's own auth from the
// environment so the skill can write back through the verbs.
//
// The skill is expected to print one JSON object: `{decision, notes}`.
// Anything else is a dispatcher-level error — the substrate refuses to
// interpret malformed gate output.
type ClaudeCLIGateDispatcher struct {
	// BinaryPath overrides the discovered `claude` binary. Production
	// callers leave it empty; tests that exercise the subprocess path
	// without a real install point this at a shim.
	BinaryPath string

	// Model, when set, rides the argv as `--model <value>` so the reviewer
	// lane runs on an operator-chosen model (satellites.toml reviewer_model,
	// sty_c7a5d741). Empty = no flag = inherit the harness default.
	Model string

	// DefaultTimeout caps a single dispatch when GateInput.Timeout is
	// zero. Zero here disables the cap entirely — used by tests that
	// want fine-grained control.
	DefaultTimeout time.Duration

	// Fetch is the server-fetch resolution source (sty_b8de4776): when a
	// non-embedded gate is absent from the worktree materialised dir, its body
	// is fetched from the server by name instead. This makes a substrate
	// reviewer injectable without a local install — the materialised dir is an
	// OPTIONAL offline cache, not a hard requirement. Nil disables the server
	// step (resolution stays embed → local only), which keeps test fakes that
	// do not need a server simple. The CLI wires the prod fetcher.
	Fetch GateBodyFetcher
}

// GateBodyFetcher fetches a gate skill's raw SKILL.md (frontmatter + body) from
// the server by name, honouring scope precedence (the same precedence
// `skill sync` materialises by — project > workspace > system > global library
// — so a server hit is the same body the local cache would have held). ok=false
// means the server holds no such skill in ANY scope; that is not an error — the
// dispatcher then fails closed naming all three sources. A transport failure is
// returned as err. The CLI owns the implementation (it has the server/config
// access the verb layer does not); the verb only holds the seam.
type GateBodyFetcher func(ctx context.Context, skillName string) (raw []byte, ok bool, err error)

// Dispatch satisfies GateDispatcher with a `claude -p` subprocess. Story
// 11 (`audit-prose + size-budget`) layers an additional gate runner on
// top of this surface; for now `claude` is the only built-in transport.
func (c ClaudeCLIGateDispatcher) Dispatch(ctx context.Context, in GateInput) (GateOutput, error) {
	if strings.TrimSpace(in.SkillName) == "" {
		return GateOutput{}, fmt.Errorf("gate dispatch: skill_name required")
	}
	binary := c.BinaryPath
	if binary == "" {
		binary = "claude"
	}
	timeout := in.Timeout
	if timeout == 0 {
		timeout = c.DefaultTimeout
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	payload, err := json.Marshal(map[string]any{
		"story_id":      in.StoryID,
		"project_id":    in.ProjectID,
		"workspace_id":  in.WorkspaceID,
		"story_body":    in.StoryBody,
		"story_status":  in.StoryStatus,
		"recent_ledger": in.RecentLedger,
	})
	if err != nil {
		return GateOutput{}, fmt.Errorf("gate dispatch: marshal payload: %w", err)
	}

	fm, systemPrompt, err := c.resolveGate(ctx, in.WorktreeRoot, in.SkillName)
	if err != nil {
		return GateOutput{}, err
	}
	// Functional half (epic:satellites-backbone 2.2): if the gate carries a
	// `check:`, the harness runs it and folds its deterministic result into the
	// gate's context. The harness only RUNS it — the gate's body owns how the
	// result combines with the LLM judgment.
	if check := strings.TrimSpace(fm.Check); check != "" {
		code, out := runGateCheck(ctx, in.WorktreeRoot, check)
		systemPrompt = appendFunctionalCheck(systemPrompt, check, code, out)
	}

	cmd := exec.CommandContext(ctx, binary, gateClaudeArgs(systemPrompt, c.Model)...)
	cmd.Stdin = strings.NewReader(string(payload))
	if in.WorktreeRoot != "" {
		cmd.Dir = in.WorktreeRoot
	}
	// Inherit the caller's environment — claude needs PATH/HOME and the
	// operator's auth to run at all. The gate skill enacts its transition
	// under that same inherited operator auth (the server authorizes
	// status_transition / review_* by the admin user); no separate reviewer
	// key is layered on.
	cmd.Env = os.Environ()

	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return GateOutput{}, fmt.Errorf("gate dispatch: subprocess exited: %s: %s",
				exitErr.String(), strings.TrimSpace(string(exitErr.Stderr)))
		}
		return GateOutput{}, fmt.Errorf("gate dispatch: subprocess: %w", err)
	}
	return ParseGateOutput(out)
}

// gateAllowedTools is the tool grant the gate subprocess runs under. A
// done-review gate must build the worktree and run its tests to honour its
// rubric ("a criterion that claims a test exists is met only if that test
// runs and passes"); without a grant the headless `claude -p` run cannot
// touch Bash and silently false-accepts on a static read (sty_cba1d47b).
// Bash covers go build/test, git, and the satellites CLI; Read/Grep/Glob
// let the gate inspect the tree. In `-p` mode a tool absent from this list
// is denied (not prompted), so this allowlist is also the scope ceiling —
// the gate can verify but cannot, e.g., Edit the tree under review. The
// same grant rides along to plan-review, which simply does not use Bash
// (harmless). Space-separated per the claude CLI's --allowedTools format.
const gateAllowedTools = "Bash Read Grep Glob"

// gateClaudeArgs builds the claude argv for a gate run. The gate skill
// body is delivered as an appended system prompt (`--append-system-prompt`,
// a real claude flag); the story payload arrives on stdin as the prompt.
// `--allowedTools` grants the gate the means to actually run build/tests in
// the worktree (sty_cba1d47b) — without it the gate cannot verify and
// false-accepts. The earlier `--skill <name>` form was invalid — the claude
// CLI has no such flag, so every gate run died at dispatch (sty_1312d692).
// Kept as a standalone function so a test can pin the argv and catch a
// future invalid flag — or a dropped tool grant — before a live run.
func gateClaudeArgs(systemPrompt, model string) []string {
	args := []string{
		"-p",
		"--allowedTools", gateAllowedTools,
		"--append-system-prompt", systemPrompt,
	}
	if m := strings.TrimSpace(model); m != "" {
		args = append(args, "--model", m)
	}
	return args
}

// errGateSkillAbsent marks the one resolution outcome the server step may
// recover from: a non-embedded gate simply not present in the worktree
// materialised dir (the blank-repo / no-local-install case). A read error that
// is NOT a plain not-exist (a permission fault), or a parse error on a present
// body, is fatal and never masked by the server — a corrupt local cache is a
// clear failure, not a cache miss.
var errGateSkillAbsent = errors.New("gate skill absent from worktree .claude/skills")

// resolveGate resolves a gate's frontmatter + body through the full source
// chain (sty_b8de4776): embed → local materialised (offline cache) → server.
// The local dir stays the FIRST non-embedded source so a present cache costs no
// network; the server is the fallback that lets a substrate reviewer dispatch
// with no local install. A gate that resolves from NO source is a clear
// fail-closed dispatch error naming all three. An embedded gate never reaches
// the server, so the home-of-gate invariant (a gate lives in one home) holds.
func (c ClaudeCLIGateDispatcher) resolveGate(ctx context.Context, worktreeRoot, skillName string) (frontmatter.Frontmatter, string, error) {
	return resolveSkillEmbedLocalServer(ctx, c.Fetch, worktreeRoot, skillName)
}

// resolveSkillEmbedLocalServer is the shared embed → local materialised → server
// resolution for any claude -p reader of a substrate skill body (sty_b8de4776):
// the gate dispatcher AND the step summariser use it, so the source chain is
// defined once. embed/local is tried first (a present cache costs no network);
// only a plain local-miss (errGateSkillAbsent) falls through to fetch — any
// other local error (corrupt body, permission fault) is fatal and surfaced
// as-is. A nil fetch (or a name no scope holds) fails closed. An embedded skill
// never reaches the server, so the home-of-gate invariant holds.
func resolveSkillEmbedLocalServer(ctx context.Context, fetch GateBodyFetcher, worktreeRoot, skillName string) (frontmatter.Frontmatter, string, error) {
	fm, body, err := resolveGateSkill(worktreeRoot, skillName)
	if err == nil {
		return fm, body, nil // embed or local hit
	}
	if !errors.Is(err, errGateSkillAbsent) || fetch == nil {
		return frontmatter.Frontmatter{}, "", err
	}
	raw, ok, ferr := fetch(ctx, skillName)
	if ferr != nil {
		return frontmatter.Frontmatter{}, "", fmt.Errorf("gate dispatch: fetch gate skill %q from server: %w", skillName, ferr)
	}
	if !ok {
		return frontmatter.Frontmatter{}, "", fmt.Errorf("gate dispatch: gate skill %q resolves from no source (not embedded, not in .claude/skills, not on the server)", skillName)
	}
	pfm, pbody, perr := frontmatter.Parse(raw)
	if perr != nil {
		return frontmatter.Frontmatter{}, "", fmt.Errorf("gate dispatch: parse server gate skill %q: %w", skillName, perr)
	}
	return pfm, string(pbody), nil
}

// resolveGateSkill returns a gate's parsed frontmatter and its body (with
// frontmatter stripped) — the rubric delivered as the system prompt. A
// satellites-INTERNAL gate (embedded in the binary) is resolved FIRST and wins
// over any worktree copy: its governance ships protected and a user editing
// `.claude/skills/<name>` cannot alter it (epic:satellites-backbone 2.2).
// Otherwise the gate is read from the worktree
// (.claude/skills/<name>/SKILL.md); an absent skill returns errGateSkillAbsent
// so the dispatcher's resolveGate can fall through to the server fetch
// (sty_b8de4776). This function itself stops at embed → local; it is the
// embed+cache half the server step builds on (and the form callers/tests that
// need no server still use).
func resolveGateSkill(worktreeRoot, skillName string) (frontmatter.Frontmatter, string, error) {
	if raw, ok := internalGateRaw(skillName); ok {
		fm, body, err := frontmatter.Parse(raw)
		if err != nil {
			return frontmatter.Frontmatter{}, "", fmt.Errorf("gate dispatch: parse internal gate %q: %w", skillName, err)
		}
		return fm, string(body), nil
	}
	root := worktreeRoot
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	path := filepath.Join(root, ".claude", "skills", skillName, "SKILL.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return frontmatter.Frontmatter{}, "", fmt.Errorf("gate dispatch: read gate skill %q at %s: %w", skillName, path, errGateSkillAbsent)
		}
		return frontmatter.Frontmatter{}, "", fmt.Errorf("gate dispatch: read gate skill %q at %s: %w", skillName, path, err)
	}
	fm, body, err := frontmatter.Parse(raw)
	if err != nil {
		return frontmatter.Frontmatter{}, "", fmt.Errorf("gate dispatch: parse gate skill %q: %w", skillName, err)
	}
	return fm, string(body), nil
}

// resolveGateSkillBody is the body-only convenience kept for callers/tests that
// do not need the frontmatter.
func resolveGateSkillBody(worktreeRoot, skillName string) (string, error) {
	_, body, err := resolveGateSkill(worktreeRoot, skillName)
	return body, err
}

// GateResolvable reports whether a gate skill resolves from ANY source the
// dispatcher uses — embed → local materialised → server (sty_b8de4776). The
// process VALIDATORS (context review, workflow check) use it so a gate pruned
// from .claude/skills but present on the server is NOT falsely flagged as
// missing/unresolvable: the dispatcher resolves it, so the validator must
// agree. A nil fetch reduces resolution to embed → local. It reuses the shared
// resolver verbatim — mechanism only, it decides nothing, it mirrors the
// dispatcher's own resolution.
func GateResolvable(ctx context.Context, fetch GateBodyFetcher, worktreeRoot, skillName string) bool {
	_, _, err := resolveSkillEmbedLocalServer(ctx, fetch, worktreeRoot, skillName)
	return err == nil
}

// runGateCheck runs a gate's functional `check:` (a `sh -c` command) in the
// worktree and returns its exit code and trimmed combined output. The harness
// only RUNS the deterministic half; the gate's body owns how the result folds
// into the verdict. A command that cannot start (or is killed) reports a
// non-zero code so the gate never reads a failed check as a pass.
func runGateCheck(ctx context.Context, worktreeRoot, check string) (int, string) {
	cmd := exec.CommandContext(ctx, "sh", "-c", check)
	if strings.TrimSpace(worktreeRoot) != "" {
		cmd.Dir = worktreeRoot
	}
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		code = 1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if c := exitErr.ExitCode(); c >= 0 {
				code = c
			}
		}
	}
	return code, strings.TrimSpace(string(out))
}

// appendFunctionalCheck folds a functional check's deterministic result into a
// gate's system prompt as a labelled section. Pure string assembly so the
// injection is unit-testable without a subprocess; the gate's decision rule
// reads this section to combine the functional half with its judgment.
func appendFunctionalCheck(systemPrompt, check string, exitCode int, output string) string {
	var b strings.Builder
	b.WriteString(systemPrompt)
	b.WriteString("\n\n## Functional check (deterministic)\n\n")
	b.WriteString(fmt.Sprintf("The harness ran this gate's functional check; fold its result into your verdict per your decision rule:\n\n`%s`\n\nexit code: %d\n", check, exitCode))
	if strings.TrimSpace(output) != "" {
		b.WriteString("\noutput:\n```\n")
		b.WriteString(output)
		b.WriteString("\n```\n")
	}
	return b.String()
}

// ParseGateOutput is lenient on whitespace + surrounding prose but strict
// on shape — a missing `decision` or one outside {accept, reject} is a
// dispatcher-level error. Mirrors the existing reviewer/runner.go
// parseOutput tolerance. Exported so the client-side reviewer command
// parses gate output through the same owner.
//
// The gate is told to emit one bare JSON object, but a `claude -p` run does
// not reliably comply — it may wrap the decision in paragraphs of reasoning,
// and that prose can itself contain braces (e.g.
// `config/.../{skills,documents,principles}/`). Locking onto the first `{`
// discarded a real verdict the gate had reached (sty_756ad5f3). Instead we
// scan every balanced `{...}` block in the output and take the **last** one
// that unmarshals into a valid decision — the decision object the gate is
// asked to print last — so brace-bearing prose before it is ignored. When
// no block yields a valid accept/reject we still error: a verdict is never
// silently dropped, and we never default to accept.
func ParseGateOutput(raw []byte) (GateOutput, error) {
	s := strings.TrimSpace(string(raw))
	if strings.HasPrefix(s, "```") {
		if nl := strings.IndexByte(s, '\n'); nl >= 0 {
			s = s[nl+1:]
		}
		if end := strings.LastIndex(s, "```"); end >= 0 {
			s = s[:end]
		}
		s = strings.TrimSpace(s)
	}

	var found *GateOutput
	var badDecision string // last block that carried a decision outside the set
	for _, block := range balancedObjects(s) {
		var out GateOutput
		if err := json.Unmarshal([]byte(block), &out); err != nil {
			continue
		}
		switch out.Decision {
		case GateDecisionAccept, GateDecisionReject:
			d := out
			found = &d // keep the last valid decision object
		default:
			if out.Decision != "" {
				badDecision = out.Decision
			}
		}
	}
	if found != nil {
		return *found, nil
	}
	// A block carried a `decision` field but with an out-of-set value —
	// report that specifically rather than as "no object at all".
	if badDecision != "" {
		return GateOutput{}, fmt.Errorf("gate dispatch: invalid decision %q (want accept|reject)", badDecision)
	}
	return GateOutput{}, fmt.Errorf("gate dispatch: parse output: no valid decision object (want one {\"decision\":\"accept|reject\"}) (raw=%q)", raw)
}

// balancedObjects returns every top-level balanced `{...}` substring of s,
// in order, honouring JSON string quoting so braces inside a string value
// do not throw off the depth count. An opening `{` that never balances is
// skipped — surrounding prose with a stray brace yields no candidate rather
// than swallowing the rest of the output.
func balancedObjects(s string) []string {
	var blocks []string
	for i := 0; i < len(s); i++ {
		if s[i] != '{' {
			continue
		}
		depth := 0
		inStr := false
		esc := false
		for j := i; j < len(s); j++ {
			c := s[j]
			if inStr {
				switch {
				case esc:
					esc = false
				case c == '\\':
					esc = true
				case c == '"':
					inStr = false
				}
				continue
			}
			switch c {
			case '"':
				inStr = true
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					blocks = append(blocks, s[i:j+1])
					i = j // resume scanning after this block
					goto next
				}
			}
		}
	next:
	}
	return blocks
}

// Gate decision discriminators emitted by gate skills and recorded in
// the ledger as the canonical accept/reject signal.
const (
	GateDecisionAccept = "accept"
	GateDecisionReject = "reject"
)
