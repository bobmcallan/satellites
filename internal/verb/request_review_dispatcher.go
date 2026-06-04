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
	NextStatus   string
	Dynamic      bool
	RecentLedger []ledger.Entry
	WorktreeRoot string
	Timeout      time.Duration
}

// GateOutput is what the dispatcher returns. NextStatus is honoured only
// when the input had Dynamic=true — declarative dispatchers can leave it
// empty and the verb fills it from the workflow's `to`.
type GateOutput struct {
	Decision   string `json:"decision"`
	Notes      string `json:"notes,omitempty"`
	NextStatus string `json:"next_status,omitempty"`
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
// The skill is expected to print one JSON object: `{decision, notes,
// next_status?}`. Anything else is a dispatcher-level error — the
// substrate refuses to interpret malformed gate output.
type ClaudeCLIGateDispatcher struct {
	// BinaryPath overrides the discovered `claude` binary. Production
	// callers leave it empty; tests that exercise the subprocess path
	// without a real install point this at a shim.
	BinaryPath string

	// DefaultTimeout caps a single dispatch when GateInput.Timeout is
	// zero. Zero here disables the cap entirely — used by tests that
	// want fine-grained control.
	DefaultTimeout time.Duration
}

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
		"next_status":   in.NextStatus,
		"dynamic":       in.Dynamic,
		"recent_ledger": in.RecentLedger,
	})
	if err != nil {
		return GateOutput{}, fmt.Errorf("gate dispatch: marshal payload: %w", err)
	}

	systemPrompt, err := resolveGateSkillBody(in.WorktreeRoot, in.SkillName)
	if err != nil {
		return GateOutput{}, err
	}

	cmd := exec.CommandContext(ctx, binary, gateClaudeArgs(systemPrompt)...)
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
func gateClaudeArgs(systemPrompt string) []string {
	return []string{
		"-p",
		"--allowedTools", gateAllowedTools,
		"--append-system-prompt", systemPrompt,
	}
}

// resolveGateSkillBody reads the gate skill's SKILL.md from the worktree
// (.claude/skills/<name>/SKILL.md) and returns its body with frontmatter
// stripped — the rubric the gate runs under, delivered as the system
// prompt. A missing skill is a dispatch error: the gate cannot run a
// skill that is not materialised in the tree under review.
func resolveGateSkillBody(worktreeRoot, skillName string) (string, error) {
	root := worktreeRoot
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	path := filepath.Join(root, ".claude", "skills", skillName, "SKILL.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("gate dispatch: read gate skill %q at %s: %w", skillName, path, err)
	}
	_, body, err := frontmatter.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("gate dispatch: parse gate skill %q: %w", skillName, err)
	}
	return string(body), nil
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
