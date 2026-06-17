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
// Cross-repo (sty_448d2024): a target OUTSIDE this repo that lands in ANOTHER
// satellites-governed repo (one with its own .satellites/) is gated against THAT
// repo's engagement — not blanket-ungated for being "outside this repo". A target
// in no governed repo (e.g. ~/.claude) stays ungated for self-maintenance.
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
	hookCmd.AddCommand(gate, newCommitGateCmd(), newStopCheckCmd(), newAccessCmd(), newPromptCmd(), newCodeNudgeCmd(), newHookContextCmd())
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

// toolInputPath is the subset of tool_input the hooks need. Edit/Write/MultiEdit
// carry file_path; NotebookEdit carries notebook_path; Bash carries command (the
// commit-gate reads it).
type toolInputPath struct {
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
	Command      string `json:"command"`
	Pattern      string `json:"pattern"` // Grep tool's search pattern (codenudge reads it)
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
	allow, reason, _ := gateOutcomeEng(start, session, ev.ToolInput.path(), now)
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
	// not Claude's self-maintenance. A target we cannot determine (empty) falls
	// through to THIS repo's engagement check (fail safe).
	if abs := absTargetPath(start, target); abs != "" {
		cfg, _, _ := cliconfig.Load(filepath.Join(root, ".satellites", "satellites.toml"))
		// An explicit ungated_dirs path is exempt regardless of repo (operator opt-in).
		if pathInUngatedDirs(abs, root, cfg.UngatedDirs) {
			return true, "", workstate.Engagement{}
		}
		if !isInsideRepo(abs, root) {
			// Outside THIS repo. Cross-repo rule (sty_448d2024): a mutation into
			// ANOTHER satellites-governed repo (one with its own
			// .satellites/satellites.toml) is gated against THAT repo's engagement —
			// keying on "is the target another GOVERNED repo", not merely "outside
			// this repo". A target in no governed repo (e.g. ~/.claude — Claude's own
			// config + agent memory) stays ungated (self-maintenance).
			if other, ok := governingRepoRoot(abs); ok && filepath.Clean(other) != filepath.Clean(root) {
				return engagementOutcome(other, session, now, crossRepoDeny(abs, other))
			}
			return true, "", workstate.Engagement{}
		}
		// Inside this repo and not ungated → fall through to the engagement check.
	}
	return engagementOutcome(root, session, now, inRepoDeny())
}

// engDeny carries the four deny messages the engagement check emits, so the
// in-repo and cross-repo callers can phrase the same outcomes for their context.
type engDeny struct{ unavailable, unreadable, none, stale, notEditable string }

// inRepoDeny is the START door's own deny vocabulary (editing the engaged repo).
func inRepoDeny() engDeny {
	return engDeny{
		unavailable: "satellites engagement store unavailable — cannot verify an active engagement (fail closed). Run `satellites work init <story>` (and `satellites update` if the client is stale).",
		unreadable:  "satellites engagement store unreadable — blocking. Run `satellites work init <story>`.",
		none:        "No active engagement for this session. Run `satellites work init <story>` to engage a story's workflow before editing code.",
		stale:       "Your engagement's lease has expired (stale). Re-engage with `satellites work init <story>` before editing.",
		notEditable: "The engaged story is not in an editable phase (e.g. backlog/done). Transition it to an editable status, or `satellites work init` the story you are actually working.",
	}
}

// crossRepoDeny is the deny vocabulary for a mutation into ANOTHER governed repo:
// the way out is to engage a story IN that repo, not this one.
func crossRepoDeny(abs, other string) engDeny {
	pre := fmt.Sprintf("satellites: `%s` is in another satellites-governed repo (%s). ", abs, other)
	return engDeny{
		unavailable: pre + "Its engagement store is unavailable — cannot verify an engagement there (fail closed).",
		unreadable:  pre + "Its engagement store is unreadable — blocking.",
		none:        pre + "This session holds no engagement there. Run `satellites work init <story>` INSIDE that repo before editing it.",
		stale:       pre + "Your engagement there has expired (stale). Re-engage with `satellites work init <story>` INSIDE that repo.",
		notEditable: pre + "The engaged story there is not in an editable phase. Advance it (or engage the story you are actually working) INSIDE that repo.",
	}
}

// engagementOutcome is the store-backed lease/editable decision for one repo
// root: an edit is allowed only under a LEASE-FRESH, EDITABLE engagement for this
// session in that repo's store. Fails closed (store unavailable/unreadable deny).
// Shared by the in-repo START door and the cross-repo rule (sty_448d2024).
func engagementOutcome(root, session string, now time.Time, deny engDeny) (bool, string, workstate.Engagement) {
	store, err := workstate.Open(stateDBForRoot(root))
	if err != nil {
		return false, deny.unavailable, workstate.Engagement{}
	}
	defer store.Close()

	engs, err := store.LiveEngagement(session)
	if err != nil {
		return false, deny.unreadable, workstate.Engagement{}
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
		return false, deny.none, workstate.Engagement{}
	case !sawFresh:
		return false, deny.stale, workstate.Engagement{}
	default:
		return false, deny.notEditable, workstate.Engagement{}
	}
}

// governingRepoRoot reports the satellites-governed repo root that CONTAINS abs
// (the directory holding .satellites/satellites.toml at or above abs), if any. A
// file (or not-yet-existing) target is resolved from its parent directory.
func governingRepoRoot(abs string) (string, bool) {
	dir := abs
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		dir = filepath.Dir(abs)
	}
	return findSatellitesRepoRoot(dir)
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

// isInsideRepo reports whether an absolute target path is within the repo tree
// rooted at root (root itself counts). Pure over its inputs.
func isInsideRepo(abs, root string) bool {
	root = filepath.Clean(root)
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// pathInUngatedDirs reports whether abs matches an explicit ungated_dirs entry
// (filepath glob; a relative entry is repo-relative; a leading ~ → $HOME). This
// is the operator's opt-in exemption, separate from the repo-boundary rule.
func pathInUngatedDirs(abs, root string, ungatedDirs []string) bool {
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
			d = filepath.Join(filepath.Clean(root), d)
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

// pathIsUngated reports whether an absolute target path is exempt from the
// START-door's per-repo engagement check: a path NOT under the repo root (the
// default boundary — Claude self-maintenance), OR an explicit ungated_dirs
// entry. NOTE: the cross-repo rule (sty_448d2024) layers ON TOP of this in
// gateOutcomeEng — an "outside this repo" path that lands in ANOTHER governed
// repo is still gated against that repo. Pure over its inputs.
func pathIsUngated(abs, root string, ungatedDirs []string) bool {
	return !isInsideRepo(abs, root) || pathInUngatedDirs(abs, root, ungatedDirs)
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
