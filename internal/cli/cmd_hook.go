// `satellites hook gate` — the PreToolUse START door (epic:hook-enforcement,
// story hook-start-door). Claude Code runs this before a file-mutating tool
// (Edit/Write/MultiEdit/NotebookEdit). It is a PRESENCE ASSESSMENT ONLY: it
// asks whether the repo has an active engagement under .satellites/work and
// blocks the edit when there is none, diverting the agent to engage a story's
// workflow first. It reviews nothing and resolves no workflow — that is the
// reviewer's job at a transition, not the door's job on every edit.
//
// Fail closed: a repo with no .satellites/satellites.toml (not configured) is
// DENIED, never allowed — the door never opens on uncertainty. (A missing
// binary is caught by the install wiring `... || exit 2`, see `satellites
// init`.)
//
// Output is the Claude Code PreToolUse decision: a `deny` is emitted as JSON
// on stdout; an `allow` emits nothing (exit 0) so the tool proceeds through
// normal permissioning — the door only ever BLOCKS, it never auto-approves.

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/cliconfig"
	"github.com/bobmcallan/satellites/internal/workstate"
	"github.com/spf13/cobra"
)

func init() {
	hookCmd := &cobra.Command{
		Use:   "hook",
		Short: "Claude Code hook handlers (harness-enforced workflow doors)",
		Long: `hook holds the handlers Claude Code invokes from .claude/settings.json.
Each subcommand reads the hook event JSON on stdin and emits the hook's
decision. They are installed into a repo by ` + "`satellites init`" + `.`,
	}
	gate := &cobra.Command{
		Use:   "gate",
		Short: "PreToolUse START door — block file edits until a story is engaged",
		Long: `gate is the PreToolUse START door. It reads the PreToolUse event on
stdin and blocks a file-mutating tool when the repo has no active engagement
under .satellites/work, diverting the agent to engage a story first.

It is a presence check only — it reviews no work and resolves no workflow.
It fails closed: an unconfigured repo is denied, never allowed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHookGate(cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
	hookCmd.AddCommand(gate, newAccessCmd(), newPromptCmd())
	register(hookCmd)
}

// preToolUseInput is the slice of the Claude Code PreToolUse event the gate
// reads. We only need cwd (to locate the repo); tool_name is read for clarity
// and possible future scoping. Unknown fields are ignored.
type preToolUseInput struct {
	ToolName        string `json:"tool_name"`
	Cwd             string `json:"cwd"`
	SessionID       string `json:"session_id"`
	ParentSessionID string `json:"parent_session_id"`
}

// gateDecisionJSON is the Claude Code PreToolUse hook output for a block.
type gateDecisionJSON struct {
	HookSpecificOutput gateHookOutput `json:"hookSpecificOutput"`
}

type gateHookOutput struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason"`
}

// runHookGate reads the PreToolUse event, assesses engagement, and emits the
// decision: a deny JSON to block, or nothing (allow) so the tool proceeds.
func runHookGate(in io.Reader, out io.Writer) error {
	raw, _ := io.ReadAll(in)
	var ev preToolUseInput
	_ = json.Unmarshal(raw, &ev) // tolerate empty/garbage; fall back to CWD

	start := strings.TrimSpace(ev.Cwd)
	if start == "" {
		if wd, err := os.Getwd(); err == nil {
			start = wd
		}
	}

	allow, reason := gateOutcome(start, sessionKey(ev.SessionID, ev.ParentSessionID), time.Now().UTC())
	if allow {
		return nil // no output → tool proceeds through normal permissioning
	}
	return emitGateDeny(out, reason)
}

// gateOutcome is the pure decision core (testable). It reads the engagement
// STORE (not engagement.json presence) keyed by the editing session: an edit is
// allowed only under a LEASE-FRESH, EDITABLE engagement for this session. A
// stale/expired/absent engagement, a candidate (access-only) row, or a
// non-editable phase is blocked. Fails closed when the repo is unconfigured or
// the store is unreadable — the store is the authority, so uncertainty denies.
func gateOutcome(start, session string, now time.Time) (allow bool, reason string) {
	root, ok := findSatellitesRepoRoot(start)
	if !ok {
		return false, "satellites is not configured here (no .satellites/satellites.toml). Run `satellites init` to set up this repo, then `satellites work init <story>` to engage a story."
	}
	store, err := workstate.Open(stateDBForRoot(root))
	if err != nil {
		return false, "satellites engagement store unavailable — cannot verify an active engagement (fail closed). Run `satellites work init <story>` (and `satellites update` if the client is stale)."
	}
	defer store.Close()

	engs, err := store.LiveEngagement(session)
	if err != nil {
		return false, "satellites engagement store unreadable — blocking. Run `satellites work init <story>`."
	}

	var sawEngagement, sawFresh bool
	for _, e := range engs {
		if e.Phase == phaseCandidate {
			continue // an access-only candidate never authorises edits
		}
		sawEngagement = true
		if !e.IsLeaseFresh(now) {
			continue
		}
		sawFresh = true
		if e.Editable {
			return true, "" // lease-fresh + editable → allow
		}
	}
	switch {
	case !sawEngagement:
		return false, "No active engagement for this session. Run `satellites work init <story>` to engage a story's workflow before editing code."
	case !sawFresh:
		return false, "Your engagement's lease has expired (stale). Re-engage with `satellites work init <story>` before editing."
	default:
		return false, "The engaged story is not in an editable phase (e.g. backlog/done). Transition it to an editable status, or `satellites work init` the story you are actually working."
	}
}

// gateWorkDir resolves the engagement directory through the shared cliconfig
// resolver, so the door reads exactly where `satellites work init` writes. A
// config it cannot load falls back to the default — the repo is already known
// to be configured (findSatellitesRepoRoot saw the toml).
func gateWorkDir(root string) string {
	cfg, _, err := cliconfig.Load(filepath.Join(root, ".satellites", "satellites.toml"))
	if err != nil {
		return filepath.Join(root, cliconfig.DefaultWorkDir)
	}
	return cfg.ResolveWorkDir(root)
}

// findSatellitesRepoRoot walks up from start looking for the repo root — the
// directory holding .satellites/satellites.toml. Mirrors cliconfig's walk-up
// but from an explicit start (the hook event's cwd), not the process CWD.
func findSatellitesRepoRoot(start string) (string, bool) {
	dir := strings.TrimSpace(start)
	if dir == "" {
		return "", false
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".satellites", "satellites.toml")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// engagement is the .satellites/work/engagement.json read-contract: the story
// whose workflow is currently engaged in this worktree. The gate treats a
// parseable record with a non-empty story_id as an active engagement. The
// writer (`satellites work init <story>`) is a separate story; the door only
// READS this file. See docs/work-engagement.md.
type engagement struct {
	StoryID   string `json:"story_id"`
	Status    string `json:"status,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// readEngagement loads <workDir>/engagement.json. ok is true only for a
// present, parseable record naming a story.
func readEngagement(workDir string) (engagement, bool) {
	path := filepath.Join(workDir, "engagement.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return engagement{}, false
	}
	var e engagement
	if err := json.Unmarshal(b, &e); err != nil {
		return engagement{}, false
	}
	if strings.TrimSpace(e.StoryID) == "" {
		return engagement{}, false
	}
	return e, true
}

// emitGateDeny writes the PreToolUse block decision as JSON on stdout.
func emitGateDeny(out io.Writer, reason string) error {
	dec := gateDecisionJSON{HookSpecificOutput: gateHookOutput{
		HookEventName:            "PreToolUse",
		PermissionDecision:       "deny",
		PermissionDecisionReason: reason,
	}}
	b, err := json.Marshal(dec)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, string(b))
	return nil
}
