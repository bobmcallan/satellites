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
	hookCmd.AddCommand(gate, newAccessCmd(), newPromptCmd(), newCodeNudgeCmd())
	register(hookCmd)
}

// preToolUseInput is the slice of the Claude Code PreToolUse event the gate
// reads. cwd locates the repo; tool_input carries the edited path so the door
// can gate only the project tree (sty_11a6077c). Unknown fields are ignored.
type preToolUseInput struct {
	ToolName        string        `json:"tool_name"`
	Cwd             string        `json:"cwd"`
	SessionID       string        `json:"session_id"`
	ParentSessionID string        `json:"parent_session_id"`
	ToolInput       toolInputPath `json:"tool_input"`
}

// toolInputPath is the subset of tool_input the gate needs: the edited path.
// Edit/Write/MultiEdit carry file_path; NotebookEdit carries notebook_path.
type toolInputPath struct {
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
}

// path returns the edited target path (file_path or notebook_path), or "".
func (t toolInputPath) path() string {
	if p := strings.TrimSpace(t.FilePath); p != "" {
		return p
	}
	return strings.TrimSpace(t.NotebookPath)
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

	session := sessionKey(ev.SessionID, ev.ParentSessionID)
	now := time.Now().UTC()
	allow, reason, eng := gateOutcomeEng(start, session, ev.ToolInput.path(), now)
	if allow {
		// Real-time activity (epic:dynamic-workflow-status, order:1): on an
		// allowed edit, a throttled touch keeps the portal's activity indicator
		// lit while the agent is editing. Best-effort and only when the allow was
		// granted by a concrete engagement (eng.Story set) — a boundary/ungated
		// allow carries no engagement, so nothing to touch.
		if root, ok := findSatellitesRepoRoot(start); ok {
			touchEngagementActivity(root, filepath.Join(root, ".satellites", "satellites.toml"), session, eng, now)
		}
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
func gateOutcome(start, session, target string, now time.Time) (allow bool, reason string) {
	allow, reason, _ = gateOutcomeEng(start, session, target, now)
	return allow, reason
}

// gateOutcomeEng is gateOutcome plus the matched engagement on allow — the
// (session, story) row the START door opened on — so the caller can record a
// real-time activity touch against it (epic:dynamic-workflow-status, order:1).
// The engagement is the zero value on a deny and on a boundary/ungated allow
// (no engagement consulted), and the touch helper no-ops on an empty story.
func gateOutcomeEng(start, session, target string, now time.Time) (allow bool, reason string, eng workstate.Engagement) {
	root, ok := findSatellitesRepoRoot(start)
	if !ok {
		return false, "satellites is not configured here (no .satellites/satellites.toml). Run `satellites init` to set up this repo, then `satellites work init <story>` to engage a story.", workstate.Engagement{}
	}
	// Boundary rule (sty_11a6077c): the door governs the engaged PROJECT tree,
	// not Claude's self-maintenance. An edit whose target resolves OUTSIDE the
	// repo root (e.g. ~/.claude — Claude's own config + agent memory), or into a
	// configured ungated_dirs path, is allowed regardless of engagement. The
	// repo's own .claude/ is inside the root, so it stays gated. A target we
	// cannot determine (empty) falls through to the engagement check (fail safe).
	if abs := absTargetPath(start, target); abs != "" {
		cfg, _, _ := cliconfig.Load(filepath.Join(root, ".satellites", "satellites.toml"))
		if pathIsUngated(abs, root, cfg.UngatedDirs) {
			return true, "", workstate.Engagement{}
		}
	}
	store, err := workstate.Open(stateDBForRoot(root))
	if err != nil {
		return false, "satellites engagement store unavailable — cannot verify an active engagement (fail closed). Run `satellites work init <story>` (and `satellites update` if the client is stale).", workstate.Engagement{}
	}
	defer store.Close()

	engs, err := store.LiveEngagement(session)
	if err != nil {
		return false, "satellites engagement store unreadable — blocking. Run `satellites work init <story>`.", workstate.Engagement{}
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
			return true, "", e // lease-fresh + editable → allow
		}
	}
	switch {
	case !sawEngagement:
		return false, "No active engagement for this session. Run `satellites work init <story>` to engage a story's workflow before editing code.", workstate.Engagement{}
	case !sawFresh:
		return false, "Your engagement's lease has expired (stale). Re-engage with `satellites work init <story>` before editing.", workstate.Engagement{}
	default:
		return false, "The engaged story is not in an editable phase (e.g. backlog/done). Transition it to an editable status, or `satellites work init` the story you are actually working.", workstate.Engagement{}
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

// absTargetPath resolves the edited path to a cleaned absolute path. A relative
// path is joined to the event cwd (where the tool runs); an absolute path is
// cleaned verbatim. Empty in → empty out (the caller falls through to the
// engagement check).
func absTargetPath(cwd, target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	if strings.HasPrefix(target, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			target = home + target[1:]
		}
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(strings.TrimSpace(cwd), target)
	}
	return filepath.Clean(target)
}

// pathIsUngated reports whether an absolute target path is exempt from the
// START-door. The default boundary: a path NOT under the repo root is ungated
// (Claude self-maintenance — ~/.claude, agent memory). Beyond that, an explicit
// ungated_dirs entry (filepath glob, leading ~ → $HOME) ungates a path even
// inside the repo. Pure over its inputs.
func pathIsUngated(abs, root string, ungatedDirs []string) bool {
	root = filepath.Clean(root)
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return true // outside the repo tree — not the door's business
	}
	home, _ := os.UserHomeDir()
	for _, d := range ungatedDirs {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		if strings.HasPrefix(d, "~") && home != "" {
			d = home + d[1:]
		}
		// A relative entry is repo-relative (e.g. "docs/scratch" → <root>/docs/scratch).
		if !filepath.IsAbs(d) {
			d = filepath.Join(root, d)
		}
		d = filepath.Clean(d)
		// Under an exempt dir, equal to it, or a glob match.
		if abs == d || strings.HasPrefix(abs, d+string(filepath.Separator)) {
			return true
		}
		if ok, _ := filepath.Match(d, abs); ok {
			return true
		}
	}
	return false
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
