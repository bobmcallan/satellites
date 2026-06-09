// `satellites work init <story>` — engage a story's workflow in this worktree
// (epic:hook-enforcement, story work-init). It is the writer the START-door
// hook diverts to: it ensures the work directory exists and records the
// engagement the gate reads. Deliberately simple — it sets up .satellites/work
// and names the engaged story; it does not (yet) validate the story against the
// substrate or resolve a workflow.
//
// The work directory is satellites.toml-configurable (work_dir) with a default
// of .satellites/work — the toml need not carry the key. Reader (hook) and
// writer (here) resolve it through cliconfig.ResolveWorkDir so they agree.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/cliconfig"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workflow"
	"github.com/bobmcallan/satellites/internal/workstate"
	"github.com/spf13/cobra"
)

func init() {
	work := &cobra.Command{
		Use:   "work",
		Short: "Per-worktree engagement state (the .satellites/work the START door reads)",
		Long: `work manages the per-worktree engagement state under .satellites/work —
which story the agent is currently working on. The START-door hook
(satellites hook gate) reads it to decide whether a file edit may proceed.`,
	}

	var (
		configArg  string
		status     string
		sessionArg string
	)
	initCmd := &cobra.Command{
		Use:   "init <story-id>",
		Short: "Engage a story — record the engagement in the per-repo event store",
		Long: `init engages a story's workflow in this worktree. It records the engagement
in the per-repo event store (keyed by session) and writes engagement.json for the
legacy START door. The session is resolved from $CLAUDE_CODE_SESSION_ID (the
harness exposes it), overridable with --session, default "local".

A session may have only ONE open story: init refuses a second different story
under a fresh lease and points at ` + "`satellites work close`" + `.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, workDir := resolveWorkContext(configArg)
			editable := resolveEditable(cmd.Context(), configArg, args[0], status)
			if err := runWorkInit(cmd.OutOrStdout(), repoRoot, workDir, resolveStateDB(configArg),
				args[0], status, resolveSession(sessionArg), editable, time.Now().UTC()); err != nil {
				return err
			}
			// Real-time activity (epic:dynamic-workflow-status, order:1): push the
			// engage event to the server now so the portal lights up immediately,
			// rather than waiting for the next batch `work sync`.
			realtimeEmitFn(cmd.Context(), configArg)
			return nil
		},
	}
	initCmd.Flags().StringVar(&configArg, "config", "", "Path to satellites.toml (resolves repo root + work_dir; defaults to walk-up from CWD).")
	initCmd.Flags().StringVar(&status, "status", "", "Optional workflow status to record as the engagement phase (advisory).")
	initCmd.Flags().StringVar(&sessionArg, "session", "", "Session key for the engagement (default $CLAUDE_CODE_SESSION_ID, else 'local').")
	work.AddCommand(initCmd)

	var closeConfigArg, closeSessionArg string
	closeCmd := &cobra.Command{
		Use:   "close <story-id>",
		Short: "Close a story's engagement — clear it from the store so it no longer vouches",
		Long: `close ends a story's engagement for this session: it records a close event
(removing the store row) and clears engagement.json when it names the story. A
finished story then leaves no leftover that could authorise later edits.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, workDir := resolveWorkContext(closeConfigArg)
			return runWorkClose(cmd.OutOrStdout(), repoRoot, workDir, resolveStateDB(closeConfigArg),
				args[0], resolveSession(closeSessionArg), time.Now().UTC())
		},
	}
	closeCmd.Flags().StringVar(&closeConfigArg, "config", "", "Path to satellites.toml (defaults to walk-up from CWD).")
	closeCmd.Flags().StringVar(&closeSessionArg, "session", "", "Session key (default $CLAUDE_CODE_SESSION_ID, else 'local').")
	work.AddCommand(closeCmd)

	var statusConfigArg string
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show the open engagements recorded in this repo's event store",
		Long: `status renders the open engagements from the per-repo engagement event
store (.satellites/state.db by default, or satellites.toml state_db) — what
each session is working on, its phase, and whether its lease is fresh. It opens
the store read-only and self-initialises an empty one if none exists.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkStatus(cmd.OutOrStdout(), resolveStateDB(statusConfigArg))
		},
	}
	statusCmd.Flags().StringVar(&statusConfigArg, "config", "", "Path to satellites.toml (resolves repo root + state_db; defaults to walk-up from CWD).")
	work.AddCommand(statusCmd)

	var syncConfigArg string
	syncCmd := &cobra.Command{
		Use:   "sync",
		Short: "Flush engagement events from the local store to the server ledger",
		Long: `sync flushes engagement events recorded since the last sync to the server
ledger via the existing ledger_append verb (kind engagement:<kind>, carrying
session_id), so the server can assemble the cross-agent view and run the
process-adherence audit. Best-effort and idempotent: a flush failure warns and is
retried next run; the server ledger stays the authority.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, _ := cliconfig.Load(syncConfigArg)
			return runWorkSync(cmd.OutOrStdout(), resolveStateDB(syncConfigArg),
				realLedgerAppender(cmd.Context(), cfg.ProjectID, syncConfigArg))
		},
	}
	syncCmd.Flags().StringVar(&syncConfigArg, "config", "", "Path to satellites.toml (defaults to walk-up from CWD).")
	work.AddCommand(syncCmd)

	register(work)
}

// resolveStateDB returns the per-repo engagement event-store path, honouring an
// optional satellites.toml state_db (default .satellites/state.db). Mirrors
// resolveWorkContext: an unconfigured repo falls back to a CWD-rooted default.
func resolveStateDB(configArg string) string {
	cfg, path, err := cliconfig.Load(configArg)
	if err != nil || strings.TrimSpace(path) == "" {
		return cfg.ResolveStateDB(cwdOrDot())
	}
	return cfg.ResolveStateDB(cliconfig.RepoRootFromConfigPath(path))
}

// runWorkStatus opens the store and renders its open engagements, newest first.
func runWorkStatus(out io.Writer, stateDB string) error {
	s, err := workstate.Open(stateDB)
	if err != nil {
		return fmt.Errorf("work status: open store: %w", err)
	}
	defer s.Close()
	engs, err := s.ListCurrent()
	if err != nil {
		return fmt.Errorf("work status: %w", err)
	}
	if len(engs) == 0 {
		fmt.Fprintln(out, "no open engagements")
		return nil
	}
	now := time.Now().UTC()
	for _, e := range engs {
		lease := "—"
		if !e.LeaseUntil.IsZero() {
			if e.LeaseUntil.After(now) {
				lease = "fresh→" + e.LeaseUntil.Format(time.RFC3339)
			} else {
				lease = "EXPIRED@" + e.LeaseUntil.Format(time.RFC3339)
			}
		}
		fmt.Fprintf(out, "%s  story=%s  phase=%s  lease=%s  session=%s\n",
			e.UpdatedAt.Format(time.RFC3339), e.Story, e.Phase, lease, e.Session)
	}
	return nil
}

// resolveWorkContext returns the repo root and the resolved work directory,
// honouring an optional satellites.toml work_dir (default .satellites/work). An
// unconfigured repo falls back to a CWD-rooted default — the zero Config still
// resolves to the default via ResolveWorkDir.
func resolveWorkContext(configArg string) (repoRoot, workDir string) {
	cfg, path, err := cliconfig.Load(configArg)
	if err != nil || strings.TrimSpace(path) == "" {
		repoRoot = cwdOrDot()
		return repoRoot, cfg.ResolveWorkDir(repoRoot)
	}
	repoRoot = cliconfig.RepoRootFromConfigPath(path)
	return repoRoot, cfg.ResolveWorkDir(repoRoot)
}

func cwdOrDot() string {
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

// engageLeaseTTL is how long an engagement's freshness lease lasts. After it
// expires the engagement is stale and (once sty_2b6cd041 lands) no longer
// authorises edits. Re-engaging the same story refreshes it.
const engageLeaseTTL = 2 * time.Hour

// resolveSession resolves the engagement session key: the --session flag, else
// the harness-exposed $CLAUDE_CODE_SESSION_ID, else "local". The access/edit
// hooks read the same session_id from their event payloads, so the writer and
// the readers agree.
func resolveSession(flag string) string {
	if s := strings.TrimSpace(flag); s != "" {
		return s
	}
	if s := strings.TrimSpace(os.Getenv("CLAUDE_CODE_SESSION_ID")); s != "" {
		return s
	}
	return "local"
}

// refreshEngagementPhase re-stamps the engaged (session, story) projection to a
// new phase + editability after a reviewer-enacted transition, so the START-door
// reads the just-advanced status instead of the stale engage-time snapshot
// (sty_2c232fa4). It is a no-op unless a live engagement already exists for the
// pair — it never fabricates one. The event carries a FRESH lease because the
// projection upserts lease_until from the event (a zero would clear it).
// Best-effort: any failure is returned for the caller to warn on, never fatal.
func refreshEngagementPhase(store *workstate.Store, session, storyID, phase string, editable bool, now time.Time) (bool, error) {
	if store == nil || strings.TrimSpace(session) == "" || strings.TrimSpace(storyID) == "" {
		return false, nil
	}
	if _, ok, err := store.Current(session, storyID); err != nil || !ok {
		return false, err
	}
	if _, err := store.Append(workstate.Event{
		Session: session, Story: storyID, Phase: strings.TrimSpace(phase),
		Kind: "phase", LeaseUntil: now.Add(engageLeaseTTL), Editable: editable, TS: now,
	}); err != nil {
		return false, err
	}
	return true, nil
}

// resolveEditable best-effort decides whether the engaged story's current phase
// permits edits, by fetching the story and asking its `## Workflow` (via
// internal/workflow.IsEditable). It NEVER over-blocks: any failure (offline, no
// workflow block, unknown status) returns true so the lease remains the hard
// gate. The door reads the stored result; this resolves it once at engage.
func resolveEditable(ctx context.Context, configPath, storyID, status string) bool {
	req, err := json.Marshal(verb.DocumentGetRequest{ID: strings.TrimSpace(storyID)})
	if err != nil {
		return true
	}
	raw, err := dispatchVerb(ctx, "document_get", req, configPath, "")
	if err != nil {
		return true // offline / not resolvable → don't over-block
	}
	var resp verb.DocumentGetResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return true
	}
	body := resp.RawBody
	if body == "" && len(resp.Versions) > 0 {
		body = resp.Versions[0].Body
	}
	if strings.TrimSpace(status) == "" {
		status = resp.Document.Status
	}
	wf, err := workflow.ParseBody([]byte(body))
	if err != nil || strings.TrimSpace(status) == "" || !wf.HasState(status) {
		return true
	}
	return wf.IsEditable(status)
}

// runWorkInit records the engagement in the per-repo store (the new authority,
// keyed by session) and writes engagement.json for the legacy door. It enforces
// single-open: a session may not open a second DIFFERENT story under a fresh
// lease. Testable core: callers pass the resolved paths, session, and a fixed now.
func runWorkInit(out io.Writer, repoRoot, workDir, stateDB, storyID, status, session string, editable bool, now time.Time) error {
	storyID = strings.TrimSpace(storyID)
	if storyID == "" {
		return fmt.Errorf("work init: story id required")
	}
	if _, err := ensureDir(workDir); err != nil {
		return err
	}

	store, err := workstate.Open(stateDB)
	if err != nil {
		return fmt.Errorf("work init: open store: %w", err)
	}
	defer store.Close()

	// Single-open: refuse a second different story while one is open under a
	// fresh lease for this session.
	engs, err := store.LiveEngagement(session)
	if err != nil {
		return fmt.Errorf("work init: %w", err)
	}
	for _, e := range engs {
		if e.Story != storyID && e.Phase != phaseCandidate && e.IsLeaseFresh(now) {
			return fmt.Errorf("work init: session already has an open story %s (phase %q). "+
				"Run `satellites work close %s` to finish it before engaging %s", e.Story, e.Phase, e.Story, storyID)
		}
	}

	if _, err := store.Append(workstate.Event{
		Session: session, Story: storyID, Phase: strings.TrimSpace(status),
		Kind: "engage", LeaseUntil: now.Add(engageLeaseTTL), Editable: editable, TS: now,
	}); err != nil {
		return fmt.Errorf("work init: record engagement: %w", err)
	}

	// Legacy engagement.json for the current START door (transitional).
	eng := engagement{StoryID: storyID, Status: strings.TrimSpace(status), UpdatedAt: now.Format(time.RFC3339)}
	b, _ := json.MarshalIndent(eng, "", "  ")
	path := filepath.Join(workDir, "engagement.json")
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("work init: write %s: %w", path, err)
	}

	fmt.Fprintf(out, "engaged %s (session %s, lease %s)\n", storyID, session, now.Add(engageLeaseTTL).Format(time.RFC3339))
	return nil
}

// runWorkClose ends a story's engagement for a session: a close event clears the
// store row, and engagement.json is removed when it names the story.
func runWorkClose(out io.Writer, repoRoot, workDir, stateDB, storyID, session string, now time.Time) error {
	storyID = strings.TrimSpace(storyID)
	if storyID == "" {
		return fmt.Errorf("work close: story id required")
	}
	store, err := workstate.Open(stateDB)
	if err != nil {
		return fmt.Errorf("work close: open store: %w", err)
	}
	defer store.Close()
	if _, err := store.Append(workstate.Event{Session: session, Story: storyID, Kind: workstate.KindClose, TS: now}); err != nil {
		return fmt.Errorf("work close: %w", err)
	}

	// Clear the legacy file when it names this story.
	path := filepath.Join(workDir, "engagement.json")
	if b, rerr := os.ReadFile(path); rerr == nil {
		var eng engagement
		if json.Unmarshal(b, &eng) == nil && strings.TrimSpace(eng.StoryID) == storyID {
			_ = os.Remove(path)
		}
	}
	fmt.Fprintf(out, "closed %s (session %s)\n", storyID, session)
	return nil
}
