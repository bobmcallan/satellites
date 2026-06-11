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

	// The commit door (sty_946fc605, epic:enforcement-surface). PreToolUse on
	// Bash: blocks `git commit`/`git push` without a lease-fresh editable
	// engagement, closing the leak where edits done via shell + a commit/push
	// slip past the edit-only START door. Fails closed (`|| exit 2`) like the
	// START door — a broken/absent client blocks the share rather than allowing
	// it. The handler itself allows all non-git and read-only git Bash.
	commitGateMatcher = "Bash"
	commitGateCommand = "satellites hook commitgate || exit 2"

	// The story-ACCESS trigger (sty_79af820a). PreToolUse on the satellites MCP
	// story-fetch + UserPromptSubmit on a story id in the prompt. These are
	// ADVISORY (no `|| exit 2`): a failure must not block a read, and the handler
	// itself fails open — the door is the hard gate, this only reminds.
	accessMatcher = "mcp__satellites__document_get"
	accessCommand = "satellites hook access"
	promptCommand = "satellites hook prompt"

	// The code-search adoption nudge (sty_f52f4b9d, epic:code-index). PreToolUse
	// on Read: ADVISORY (no `|| exit 2`) — it suggests `satellites code
	// symbol`/`search` for large indexed source files and must never block a Read.
	codeNudgeMatcher = "Read"
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
}

func init() {
	var configArg string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold this repo for satellites and install the workflow hook",
		Long: `init makes a repo ready for satellites, idempotently. It ensures:

  - the .satellites/ directory,
  - a satellites.toml (created if missing, left intact if present),
  - the PreToolUse START-door + advisory hooks in .claude/settings.json,
  - a SessionStart hook that runs ` + "`satellites code index`" + ` so the
    code symbol index is refreshed deterministically each session.

Author NEW skills as files under .satellites/skills/ and push them with
` + "`satellites skill upload`" + ` (review-gated). The .claude/skills/ tree is
sync-owned (` + "`satellites skill sync`" + ` pulls into it) — never hand-write there.

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

	// 2b. Trunk workflow scaffold (epic:enforcement-surface, order:2): when the
	//     repo has no kind:workflow source, write a minimal trunk workflow + its
	//     gate skills into .satellites/skills/ so the START door enforces a real,
	//     advanceable process instead of engage-only theatre. Operator-owned
	//     source the operator edits + uploads — not a runtime default.
	if err := scaffoldWorkflowIfAbsent(out, satDir); err != nil {
		return err
	}

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
	return nil
}

const scaffoldToml = `# satellites.toml — repo config (non-secret). Run ` + "`satellites auth`" + ` to add credentials.
# server_url = "https://your-satellites-server"
# project_id = "proj_..."
# data_dir = ".satellites"        # home for the client data stores: state.db + index.db (default; optional)
# work_dir = ".satellites/work"   # per-story working area, e.g. evidence review outputs (default; optional)
` + ungatedDirsBlock

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
