// `satellites init` — scaffold a repo for satellites and install the
// harness-enforced START door (epic:hook-enforcement, story hook-install).
// Idempotent: it ensures the .satellites/ directory, a satellites.toml, and
// the PreToolUse hook in .claude/settings.json, reporting what it added versus
// what was already present and never clobbering existing settings.

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

	// The story-ACCESS trigger (sty_79af820a). PreToolUse on the satellites MCP
	// story-fetch + UserPromptSubmit on a story id in the prompt. These are
	// ADVISORY (no `|| exit 2`): a failure must not block a read, and the handler
	// itself fails open — the door is the hard gate, this only reminds.
	accessMatcher = "mcp__satellites__document_get"
	accessCommand = "satellites hook access"
	promptCommand = "satellites hook prompt"
)

// installedHook describes one hook init merges into .claude/settings.json.
type installedHook struct {
	event   string // "PreToolUse" / "UserPromptSubmit"
	matcher string // "" ⇒ no matcher (e.g. UserPromptSubmit)
	command string
	label   string
}

// hooksToInstall is the set init ensures, idempotently: the START door plus the
// advisory story-access triggers.
var hooksToInstall = []installedHook{
	{"PreToolUse", hookMatcher, hookCommand, ".claude/settings.json (PreToolUse START-door hook)"},
	{"PreToolUse", accessMatcher, accessCommand, ".claude/settings.json (PreToolUse story-access reminder)"},
	{"UserPromptSubmit", "", promptCommand, ".claude/settings.json (UserPromptSubmit story-access reminder)"},
}

func init() {
	var configArg string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold this repo for satellites and install the workflow hook",
		Long: `init makes a repo ready for satellites, idempotently. It ensures:

  - the .satellites/ directory,
  - a satellites.toml (created if missing, left intact if present),
  - the PreToolUse START-door hook in .claude/settings.json.

Re-running is safe: existing files and settings are preserved and the hook is
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
		fmt.Fprintln(out, initLine(false, ".satellites/satellites.toml"))
	case os.IsNotExist(statErr):
		if werr := os.WriteFile(tomlPath, []byte(scaffoldToml), 0o644); werr != nil {
			return fmt.Errorf("init: write %s: %w", tomlPath, werr)
		}
		fmt.Fprintln(out, initLine(true, ".satellites/satellites.toml"))
	default:
		return fmt.Errorf("init: stat %s: %w", tomlPath, statErr)
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
# work_dir = ".satellites/work"   # where the START-door engagement state lives (default; optional)
`

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
