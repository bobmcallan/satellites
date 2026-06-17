// `satellites init` — scaffold a repo for satellites and install the
// harness-enforced START door (epic:hook-enforcement, story hook-install).
// Idempotent: it ensures the .satellites/ directory, a satellites.toml, and the
// harness hooks in .claude/settings.json (the PreToolUse START door + advisory
// triggers and the SessionStart code-index refresh), reporting what it added
// versus what was already present and never clobbering existing settings.

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bobmcallan/satellites/internal/cliconfig"
	"github.com/spf13/cobra"
)

// The PreToolUse hook init installs. The matcher scopes it to file-mutating
// tools; the command runs the START door and FAILS CLOSED (`|| exit 2`) if the
// satellites binary is missing or errors, so a broken/absent client blocks the
// edit rather than silently allowing it (exit 2 = blocking error in Claude
// Code's hook protocol).
const (
	hookMatcher = "Edit|Write|MultiEdit|NotebookEdit"
	hookCommand = "satellites hook gate || exit 2"

	// The Bash door (sty_946fc605 + sty_448d2024, epic:enforcement-surface).
	// PreToolUse on Bash, enforcing two things without a lease-fresh editable
	// engagement: (1) obvious in-repo file-mutating Bash forms — output
	// redirection (`>`/`>>`), `tee`, `mv`/`cp`/`rm`/`sed -i`/`git mv` — which the
	// edit-only START door (Edit|Write) does not match; and (2) the share point,
	// `git push` (or `git commit` under the strict commit_gate knob). Targets
	// resolve through the same boundary/ungated_dirs/cross-repo rules, so /tmp,
	// reads, builds and non-governed paths are not gated. Fails closed
	// (`|| exit 2`) like the START door. Heuristic, not a shell parser — the share
	// gate is the catch-all backstop.
	commitGateMatcher = "Bash"
	commitGateCommand = "satellites hook commitgate || exit 2"

	// The story-ACCESS trigger (sty_79af820a). PreToolUse on the satellites MCP
	// story-fetch + UserPromptSubmit on a story id in the prompt. These are
	// ADVISORY (no `|| exit 2`): a failure must not block a read, and the handler
	// itself fails open — the door is the hard gate, this only reminds.
	accessMatcher = "mcp__satellites__document_get"
	accessCommand = "satellites hook access"
	promptCommand = "satellites hook prompt"

	// The code-search adoption nudge (sty_f52f4b9d / sty_097bf1a3, epic:code-
	// index-replacement). PreToolUse on Read|Grep|Bash: ADVISORY (no `|| exit 2`)
	// — it steers a large-source Read, a symbol-shaped Grep, or a symbol-shaped
	// grep/rg via Bash toward `satellites code symbol`/`search`, and must never
	// block. One alternation matcher (not three entries) because the merge keys
	// idempotency by command; the handler dispatches on tool_name.
	codeNudgeMatcher = "Read|Grep|Bash"
	codeNudgeCommand = "satellites hook codenudge"

	// The deterministic code-index refresh (sty_a89da7e4, epic:code-index).
	// SessionStart: builds/refreshes .satellites/index.db at the top of every
	// session so the index is fresh WITHOUT the agent having to remember to run
	// it (and without any CLAUDE.md prose). Incremental and cheap — a no-op-fast
	// pass when nothing changed. ADVISORY: no `|| exit 2`, so an index hiccup can
	// never block a session; the codenudge handler stays silent until this has
	// produced an index for it to point at.
	sessionIndexCommand = "satellites code index"

	// The always-context injector (sty_a29e1845, epic:always-context).
	// SessionStart: injects the always-flagged (principles:always) docs +
	// principles and the standing `satellites document index` pointer, so the
	// small resident set is in front of the agent every session WITHOUT any
	// CLAUDE.md prose. ADVISORY: no `|| exit 2`, and the handler fails open, so
	// a fetch hiccup never blocks a session.
	sessionContextCommand = "satellites hook context"

	// The commit-without-gate Stop advisory (sty_b713f886, epic:enforcement-
	// surface). Stop: at end of turn it warns when the session committed work but
	// its engaged story is still non-terminal. ADVISORY — no `|| exit 2`, and the
	// handler always exits 0, because a Stop hook that errors would block the stop
	// (it cannot un-push, so it must never block).
	stopCheckCommand = "satellites hook stopcheck"
)

// installedHook describes one hook init merges into .claude/settings.json.
type installedHook struct {
	event   string // "PreToolUse" / "UserPromptSubmit"
	matcher string // "" ⇒ no matcher (e.g. UserPromptSubmit)
	command string
	label   string
}

// hooksToInstall is the set init ensures, idempotently: the START door, the
// advisory story-access triggers, and the SessionStart code-index refresh.
var hooksToInstall = []installedHook{
	{"PreToolUse", hookMatcher, hookCommand, ".claude/settings.json (PreToolUse START-door hook)"},
	{"PreToolUse", commitGateMatcher, commitGateCommand, ".claude/settings.json (PreToolUse commit-gate hook)"},
	{"PreToolUse", accessMatcher, accessCommand, ".claude/settings.json (PreToolUse story-access reminder)"},
	{"PreToolUse", codeNudgeMatcher, codeNudgeCommand, ".claude/settings.json (PreToolUse code-search nudge)"},
	{"PreToolUse", accessMatcher, sessionContextCommand, ".claude/settings.json (PreToolUse always-context re-anchor on story fetch)"},
	{"UserPromptSubmit", "", promptCommand, ".claude/settings.json (UserPromptSubmit story-access reminder)"},
	{"SessionStart", "", sessionIndexCommand, ".claude/settings.json (SessionStart code-index refresh)"},
	{"SessionStart", "", sessionContextCommand, ".claude/settings.json (SessionStart always-context inject)"},
	{"Stop", "", stopCheckCommand, ".claude/settings.json (Stop commit-without-gate advisory)"},
}

func init() {
	var configArg string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold this repo for satellites and install the workflow hook",
		Long: `init makes a repo ready for satellites, idempotently. It ensures:

  - the .satellites/ directory,
  - a satellites.toml (created if missing, left intact if present),
  - the documented global_publishers consumption block in the toml,
  - the PreToolUse START-door + advisory hooks in .claude/settings.json,
  - a SessionStart hook that runs ` + "`satellites code index`" + ` so the
    code symbol index is refreshed deterministically each session,
  - a .gitignore managed block keeping .satellites/ local state out of git.

init scaffolds the gateless BASELINE workflow (backlog → in_progress → done, no
gates) into .satellites/workflows/ so a fresh repo is governable out of the box —
create-if-absent, so a repo that already owns a workflow keeps it. It writes no
gates/principles/skills: those are CONSUMED. The system baseline (authoring/review
capabilities + principles shipped with satellites) is inherited automatically;
CONSUME a publisher's gates by adding its ` + "`<publisher>`" + ` to global_publishers,
then ` + "`satellites skill sync`" + ` to materialise them into .claude/skills/
(sync-owned — never hand-write there). Gates are an opt-in palette: compose them
into a richer repo-owned workflow when you want them.

Re-running is safe: existing files and settings are preserved and hooks are
not duplicated. init reports what it added versus what was already present.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd.OutOrStdout(), initRepoRoot(configArg))
		},
	}
	cmd.Flags().StringVar(&configArg, "config", "", "Path to satellites.toml (resolves the repo root; defaults to walk-up from CWD).")
	register(cmd)
}

// initRepoRoot resolves the repo to scaffold: the directory holding an existing
// .satellites/ (via config resolution), else the current directory for a fresh
// repo.
func initRepoRoot(configArg string) string {
	if _, path, err := cliconfig.Load(configArg); err == nil && path != "" {
		return cliconfig.RepoRootFromConfigPath(path)
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

// runInit performs the idempotent scaffold + hook install for repoRoot.
func runInit(out io.Writer, repoRoot string) error {
	// 1. .satellites/ directory.
	satDir := filepath.Join(repoRoot, ".satellites")
	created, err := ensureDir(satDir)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, initLine(created, ".satellites/"))

	// 2. satellites.toml — created only if absent; never overwritten.
	tomlPath := filepath.Join(satDir, "satellites.toml")
	switch _, statErr := os.Stat(tomlPath); {
	case statErr == nil:
		// Never overwrite an existing toml, but DO maintain the ungated_dirs
		// knob: append its documented block when absent (idempotent), so an
		// already-initialised repo gains the START-door exemption comment
		// (sty_11a6077c).
		appended, aerr := ensureUngatedDirsBlock(tomlPath)
		if aerr != nil {
			return fmt.Errorf("init: maintain %s: %w", tomlPath, aerr)
		}
		if appended {
			fmt.Fprintln(out, initLine(true, ".satellites/satellites.toml (added ungated_dirs note)"))
		} else {
			fmt.Fprintln(out, initLine(false, ".satellites/satellites.toml"))
		}
	case os.IsNotExist(statErr):
		if werr := os.WriteFile(tomlPath, []byte(scaffoldToml), 0o644); werr != nil {
			return fmt.Errorf("init: write %s: %w", tomlPath, werr)
		}
		fmt.Fprintln(out, initLine(true, ".satellites/satellites.toml"))
	default:
		return fmt.Errorf("init: stat %s: %w", tomlPath, statErr)
	}

	// 2b. Consumption config (epic:client-dir-separation order-4): a repo onboards
	//     by CONSUMPTION, not authoring. The system baseline is inherited
	//     automatically; a publisher's global governance (a workflow + its gates)
	//     is CONSUMED by opting into the PUBLISHER in global_publishers, then
	//     `skill sync`. init seeds the documented (commented) global_publishers
	//     block — it writes NO local governance files. This retires the per-skill
	//     library_pins.
	if appended, perr := ensureGlobalPublishersBlock(tomlPath); perr != nil {
		return perr
	} else if appended {
		fmt.Fprintln(out, initLine(true, ".satellites/satellites.toml (added global_publishers consumption note)"))
	}
	fmt.Fprintln(out, "  → governance: the order-zero baseline workflow + a starter constitution are scaffolded below; the baseline's entry is gated by the internal intent-gate (config-over-code, injected from the binary). Other gates/principles/skills are inherited (system baseline) or CONSUMED — add a `<publisher>` to global_publishers, then `satellites skill sync`.")

	// 3. The harness hooks in .claude/settings.json — the START door plus the
	//    advisory story-access triggers. Each is merged idempotently.
	settingsPath := filepath.Join(repoRoot, ".claude", "settings.json")
	for _, h := range hooksToInstall {
		added, err := ensureHookInstalled(settingsPath, h.event, h.matcher, h.command)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, initLine(added, h.label))
	}

	// 4. .gitignore — keep the client's per-repo local state (config/state, not
	//    substrate) out of git. Created if absent; otherwise a managed block is
	//    appended once. Never clobbers a user's own .gitignore (sty_b47c758c).
	if added, gerr := ensureGitignore(repoRoot); gerr != nil {
		return gerr
	} else {
		fmt.Fprintln(out, initLine(added, ".gitignore (.satellites local-state block)"))
	}

	// 5. Baseline workflow — scaffold the gateless backlog→in_progress→done
	//    baseline so a fresh repo is governable out of the box (no
	//    ungoverned-story). Create-if-absent: a repo that already owns a workflow
	//    keeps it (no overwrite, no ambiguous-governance). Gates stay an opt-in
	//    palette this baseline names none of (sty_1174c0b9).
	if added, berr := ensureBaselineWorkflow(repoRoot); berr != nil {
		return berr
	} else {
		fmt.Fprintln(out, initLine(added, ".satellites/workflows/satellites-baseline-workflow.md (intent-gated baseline)"))
	}

	// 6. Starter constitution — scaffold a repo-owned principles:always
	//    constitution the intent-gates enforce, create-if-absent so a repo that
	//    owns its principles is untouched (epic:satellites-backbone 2.4).
	if added, cerr := ensureStarterConstitution(repoRoot); cerr != nil {
		return cerr
	} else {
		fmt.Fprintln(out, initLine(added, ".satellites/principles/constitution.md (starter, principles:always)"))
	}
	return nil
}

const scaffoldToml = `# satellites.toml — repo config (non-secret). Run ` + "`satellites auth`" + ` to add credentials.
# server_url = "https://your-satellites-server"
# project_id = "proj_..."
# data_dir = ".satellites"        # home for the client data stores: state.db + index.db (default; optional)
# work_dir = ".satellites/work"   # per-story working area, e.g. evidence review outputs (default; optional)
#
# substrate_roots — per-kind authoring-source parent dir (default ".satellites",
# i.e. .satellites/<kind>). Override a kind to author it elsewhere, e.g. at the
# repo root (top-level documents/ principles/ skills/):
# [substrate_roots]
# skills = "."
` + globalPublishersBlock + ungatedDirsBlock

// globalPublishersBlock documents the consumption knob (epic:client-dir-separation
// order-4, retiring the per-skill library_pins). A repo authors no governance
// locally — the system baseline is inherited automatically, and a publisher's
// global artifacts (a workflow + its gates) are CONSUMED by opting into the
// PUBLISHER here, then materialised by ` + "`satellites skill sync`" + ` into
// .claude/skills/. Commented + repo-agnostic by default.
const globalPublishersBlock = `
# global_publishers — the publishers (project ids) whose GLOBAL artifacts this
# repo CONSUMES from the shared library. Opt into a publisher (not a hand-listed
# per-skill set) and run ` + "`satellites skill sync`" + ` to materialise its global
# governance (a workflow + its gates) into .claude/skills/. The system baseline
# is inherited with no setup; a repo authors no governance locally. This retires
# the per-skill library_pins (a remaining library_pins is still honoured as a
# deprecated fallback — its publishers are derived).
# global_publishers = ["<publisher>"]
`

// globalPublishersKey is the toml key init looks for before appending the block
// to an existing config (idempotency).
const globalPublishersKey = "global_publishers"

// ensureGlobalPublishersBlock appends the documented global_publishers consumption
// block to an existing toml when neither it nor the deprecated library_pins is
// already present. Idempotent: a second run is a no-op. Returns whether it
// appended. A freshly written toml (scaffoldToml) already carries the block.
func ensureGlobalPublishersBlock(tomlPath string) (bool, error) {
	raw, err := os.ReadFile(tomlPath)
	if err != nil {
		return false, err
	}
	for _, ln := range strings.Split(string(raw), "\n") {
		t := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(ln), "#"))
		if strings.HasPrefix(t, globalPublishersKey) || strings.HasPrefix(t, "library_pins") {
			return false, nil // already present (commented or active), or a legacy pinned repo
		}
	}
	body := string(raw)
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	if err := os.WriteFile(tomlPath, []byte(body+globalPublishersBlock), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// ungatedDirsBlock documents + seeds the START-door exemption knob. The door
// gates only edits INSIDE this repo by default — anything outside the repo root
// (e.g. ~/.claude, Claude's own config + agent memory) is ungated, so Claude can
// self-maintain. List EXTRA exempt dirs here (sty_11a6077c). Commented by
// default: the boundary rule governs until a user opts in.
const ungatedDirsBlock = `
# ungated_dirs — paths the START-door (satellites hook gate) will NOT gate.
# By default the door only gates edits INSIDE this repo; anything outside the
# repo root (e.g. ~/.claude — Claude's own config + memory) is already ungated.
# List extra dirs here to also ungate them (globs; a leading ~ expands to $HOME):
# ungated_dirs = ["~/.claude", "docs/scratch"]
`

// ungatedDirsKey is the toml key init looks for before appending the block to an
// existing config (idempotency).
const ungatedDirsKey = "ungated_dirs"

// ensureUngatedDirsBlock appends the documented ungated_dirs block to an
// existing toml when the key is not already present (commented or active).
// Idempotent: a second run is a no-op. Returns whether it appended.
func ensureUngatedDirsBlock(tomlPath string) (bool, error) {
	raw, err := os.ReadFile(tomlPath)
	if err != nil {
		return false, err
	}
	// Match the key whether active (`ungated_dirs =`) or already documented in
	// the seeded comment (`# ungated_dirs`). Scan line-wise so a substring in
	// some other value cannot false-match.
	for _, ln := range strings.Split(string(raw), "\n") {
		t := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(ln), "#"))
		if strings.HasPrefix(t, ungatedDirsKey) {
			return false, nil // already present
		}
	}
	body := string(raw)
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	if err := os.WriteFile(tomlPath, []byte(body+ungatedDirsBlock), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// gitignoreMarker opens the managed block ensureGitignore maintains. Its
// presence (anywhere in the file) means the block is already installed, so a
// re-run is a no-op — the same idempotency contract as the toml knobs.
const gitignoreMarker = "# >>> satellites (managed) >>>"

// gitignoreBlock is the managed .gitignore section. It uses the allowlist shape:
// ignore EVERYTHING under .satellites/ (the client's config/state home —
// state.db*/index.db* + WAL sidecars, logs/, the work/ engagement store,
// worktree/), then re-include the substrate AUTHORING dirs + the non-secret toml
// so those ARE committed. This is more future-proof than a denylist of known
// state files (a new state file is ignored by default), and it keeps the
// repo-owned baseline workflow under .satellites/workflows/ committable.
const gitignoreBlock = gitignoreMarker + `
# .satellites/ is the satellites client's config/state home — ignore it all
# (state.db*/index.db* + WAL sidecars, logs/, work/ engagement store, worktree/),
# then re-include the substrate authoring dirs + the non-secret toml so THOSE are
# committed. Local state is config/state, not substrate.
.satellites/*
!.satellites/documents/
!.satellites/principles/
!.satellites/skills/
!.satellites/workflows/
!.satellites/seeds/
!.satellites/satellites.toml
# <<< satellites (managed) <<<
`

// ensureGitignore writes the managed local-state block to the repo's .gitignore,
// idempotently and non-destructively: it creates the file with the block when
// absent, appends the block when the file exists without the marker, and is a
// no-op when the marker is already present. A user's own entries are preserved.
// Returns whether it wrote anything.
func ensureGitignore(repoRoot string) (bool, error) {
	path := filepath.Join(repoRoot, ".gitignore")
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if strings.Contains(string(raw), gitignoreMarker) {
			return false, nil // managed block already present
		}
		body := string(raw)
		if !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		if werr := os.WriteFile(path, []byte(body+"\n"+gitignoreBlock), 0o644); werr != nil {
			return false, fmt.Errorf("init: append %s: %w", path, werr)
		}
		return true, nil
	case os.IsNotExist(err):
		if werr := os.WriteFile(path, []byte(gitignoreBlock), 0o644); werr != nil {
			return false, fmt.Errorf("init: write %s: %w", path, werr)
		}
		return true, nil
	default:
		return false, fmt.Errorf("init: read %s: %w", path, err)
	}
}

// baselineWorkflowDoc is the gateless baseline workflow init scaffolds into a
// fresh repo: backlog → in_progress → done with NO gates, all bare ungated
// edges. It makes a clean repo governable (no ungoverned-story) without
// hand-authoring; gates remain an opt-in palette a richer repo-owned workflow
// composes. The ```yaml fences are concatenated (a Go raw string can't hold a
// backtick). Advance via `satellites story set-status` along the ungated edges.
const baselineWorkflowDoc = `---
name: satellites-baseline-workflow
kind: workflow
tags: [kind:workflow]
applies_to: ["*"]
description: The order-zero baseline lifecycle — backlog to in_progress to done. The entry is gated by the intent-gate (satellites-intent-plan-review, injected from the binary) so a story is judged against the repo's config-over-code intent before any code exists; the exit is ungated. Other gates stay an opt-in palette a richer repo-owned workflow composes.
---

# Baseline workflow (order-zero)

The minimal governing workflow: a story moves backlog to in_progress to done.
The ONLY gate is the intent spine every repo gets — the entry transition
(backlog to in_progress) is judged by satellites-intent-plan-review, which
rejects a plan that proposes baking a process, a gate, or an opinion into the
binary instead of the substrate (the config-over-code rule), honouring the
repo's resident constitution. The intent-gates are satellites-INTERNAL: injected
from the binary, never materialised to .claude/skills, so they cannot be edited.

The exit (in_progress to done) and the cancel edges are ungated — advance them
with: satellites story set-status <story-id> <status>. The goal-keeper Stop hook
holds the agent to a terminal state. Other gates (start / techdebt / done review,
and so on) remain an opt-in palette a richer repo-owned workflow composes; this
baseline names only the intent spine.

## Workflow

- backlog to in_progress — open, gated by satellites-intent-plan-review (intent).
- in_progress to done — close (ungated).
- backlog/in_progress to cancelled — abandon (ungated).

` + "```yaml" + `
states:
  - backlog
  - {name: in_progress, actor: executor}
  - done
  - cancelled
transitions:
  - {from: backlog, to: in_progress, reviewer_skill: "satellites-intent-plan-review"}
  - {from: in_progress, to: done}
  - {from: backlog, to: cancelled}
  - {from: in_progress, to: cancelled}
` + "```" + `

## Checkpoint gates

- [[satellites-intent-code-review]] — the config-over-code gate judged on the
  diff: rejects code that hardcodes a gate, process step, or opinion that belongs
  in the substrate. Injected from the binary (internal); run at the commit
  checkpoint by the repo's commit routine.

## Environment

Drives a story document backlog to in_progress to done. The entry is gated by the
internal intent-gate; the exit and cancels are ungated status moves. The
goal-keeper Stop hook holds the agent to a terminal state.

` + "```yaml" + `
guardrails:
  always:
    - Drive the engaged story to a terminal state (done) — the goal-keeper holds you to it.
    - Request the entry intent-gate (satellites-intent-plan-review) to open a story; it rejects a hardcoding plan.
  ask_first: []
  never:
    - Move a story across a gated edge with set-status — route it through the named gate.
    - Move a story across an edge its governing workflow does not declare.
` + "```" + `
`

// ensureBaselineWorkflow scaffolds the gateless baseline workflow into
// .satellites/workflows/, create-if-absent: it writes nothing when a workflow
// already exists there (so a repo that owns its workflows is never overwritten
// and no two ["*"] workflows collide as ambiguous-governance). Returns whether
// it wrote the baseline.
func ensureBaselineWorkflow(repoRoot string) (bool, error) {
	wfDir := filepath.Join(repoRoot, ".satellites", "workflows")
	if entries, err := os.ReadDir(wfDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				return false, nil // a workflow already exists — leave it
			}
		}
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("init: read %s: %w", wfDir, err)
	}
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		return false, fmt.Errorf("init: create %s: %w", wfDir, err)
	}
	path := filepath.Join(wfDir, "satellites-baseline-workflow.md")
	if err := os.WriteFile(path, []byte(baselineWorkflowDoc), 0o644); err != nil {
		return false, fmt.Errorf("init: write %s: %w", path, err)
	}
	return true, nil
}

// starterConstitutionDoc is the repo-AGNOSTIC starter constitution init/rebase
// scaffold into .satellites/principles/, create-if-absent. It is
// principles:always (resident every session) and a STARTER the repo adapts to
// its own intent; the config-over-code rule is the universal spine the intent-
// gates enforce (epic:satellites-backbone 2.4).
const starterConstitutionDoc = `---
name: constitution
tags: [principles:always]
---
# Constitution

This repo's process is configuration, not code. Workflows, gates, reviews, and
opinions live in the substrate — documents, principles, skills, and workflow
config the team authors and edits without a binary release — never as branches
baked into a binary. A check that is deterministic is still configuration: a
command a gate carries, not a hardcoded decision.

When a change proposes baking a process, a gate, or an opinion into the binary
instead of the substrate, that is the violation — move it to configuration.

Adapt the rest of this document to your repo's own intent and definition of
"good"; the rule above is the spine the intent-gates enforce.
`

// ensureStarterConstitution scaffolds the starter constitution into
// .satellites/principles/, create-if-absent: it writes nothing when that
// directory already holds any .md (a repo that owns its principles — including
// satellites itself, with satellites-constitution.md — is never overwritten and
// never gets a second resident constitution). Returns whether it wrote one.
func ensureStarterConstitution(repoRoot string) (bool, error) {
	dir := filepath.Join(repoRoot, ".satellites", "principles")
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				return false, nil // the repo already owns principles — leave them
			}
		}
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("init: read %s: %w", dir, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, fmt.Errorf("init: create %s: %w", dir, err)
	}
	path := filepath.Join(dir, "constitution.md")
	if err := os.WriteFile(path, []byte(starterConstitutionDoc), 0o644); err != nil {
		return false, fmt.Errorf("init: write %s: %w", path, err)
	}
	return true, nil
}

// initLine renders a one-line report: "+ created" or "= present".
func initLine(created bool, what string) string {
	if created {
		return "  + " + what
	}
	return "  = " + what + " (already present)"
}

// ensureDir creates dir (and parents) if absent. Returns whether it created it.
func ensureDir(dir string) (bool, error) {
	switch _, err := os.Stat(dir); {
	case err == nil:
		return false, nil
	case os.IsNotExist(err):
		if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
			return false, fmt.Errorf("init: create %s: %w", dir, mkErr)
		}
		return true, nil
	default:
		return false, fmt.Errorf("init: stat %s: %w", dir, err)
	}
}

// ensureHookInstalled merges one command hook (for the given event/matcher)
// into the Claude Code settings at path, idempotently and non-destructively.
// Returns whether it added the hook (false = already present, nothing written).
func ensureHookInstalled(path, event, matcher, command string) (bool, error) {
	var raw []byte
	if b, err := os.ReadFile(path); err == nil {
		raw = b
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("init: read %s: %w", path, err)
	}

	merged, added, err := mergeHookIntoSettings(raw, event, matcher, command)
	if err != nil {
		return false, fmt.Errorf("init: merge %s: %w", path, err)
	}
	if !added {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("init: create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, merged, 0o644); err != nil {
		return false, fmt.Errorf("init: write %s: %w", path, err)
	}
	return true, nil
}

// mergeHookIntoSettings adds a command hook for `event` to a Claude Code
// settings document, preserving everything else. existing may be empty (new
// file); matcher may be empty (omitted, e.g. UserPromptSubmit). It returns the
// new document bytes and whether anything was added (false when a hook with the
// same command is already present under that event — idempotent).
func mergeHookIntoSettings(existing []byte, event, matcher, command string) ([]byte, bool, error) {
	doc := map[string]any{}
	if len(strings.TrimSpace(string(existing))) > 0 {
		if err := json.Unmarshal(existing, &doc); err != nil {
			return nil, false, fmt.Errorf("parse settings json: %w", err)
		}
	}

	hooks, _ := doc["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	arr, _ := hooks[event].([]any)

	// Idempotency: already installed if any existing entry under this event
	// carries our command.
	for _, entry := range arr {
		em, _ := entry.(map[string]any)
		hl, _ := em["hooks"].([]any)
		for _, h := range hl {
			hm, _ := h.(map[string]any)
			if cmd, _ := hm["command"].(string); strings.TrimSpace(cmd) == command {
				return existing, false, nil
			}
		}
	}

	entry := map[string]any{
		"hooks": []any{
			map[string]any{"type": "command", "command": command},
		},
	}
	if strings.TrimSpace(matcher) != "" {
		entry["matcher"] = matcher
	}
	hooks[event] = append(arr, entry)
	doc["hooks"] = hooks

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, false, err
	}
	return append(out, '\n'), true, nil
}
