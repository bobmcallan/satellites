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
//
// `.satellites/work` is LOAD-BEARING, not vestigial (sty_8c6fb902 traced it):
//   - engagement.json — written here by `work init`, read by the START-door
//     hook (cmd_hook.go readEngagement). A tiny JSON precisely so the bash hook
//     can read the active engagement WITHOUT opening the sqlite state.db; this
//     is the hook's read-contract (docs/work-engagement.md).
//   - .satellites/work/<story>/review.md + metrics.json — written by
//     `satellites evidence review` (cmd_evidence_review.go).
// The per-story inbox/claim ROWS moved to the event store (state.db /
// workstate.Store, sty_676e070c / sty_8e8ec0e7); only the two file artifacts
// above remain on disk. Do not remove the dir or its ResolveWorkDir machinery.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
			editable, actor, resolvedStatus := resolveEngageGuards(cmd.Context(), configArg, args[0], status)
			if err := refuseNonExecutorActor("work init", args[0], resolvedStatus, actor); err != nil {
				return err
			}
			// Drive-time gate (sty_fc39ca77): the reviewer runs BEFORE you start
			// driving — the governing workflow must pass the reference dry-run, so a
			// dangling reviewer_skill / [[wikilink]] is caught and fixed rather than
			// failing closed mid-drive.
			if err := driveTimeWorkflowGate(cmd.Context(), cmd.OutOrStdout(), configArg, "", args[0]); err != nil {
				return err
			}
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
	var closeForce bool
	closeCmd := &cobra.Command{
		Use:   "close <story-id>",
		Short: "Close a story's engagement — clear it from the store so it no longer vouches",
		Long: `close ends a story's engagement for this session: it records a close event
(removing the store row) and clears engagement.json when it names the story. A
finished story then leaves no leftover that could authorise later edits.

Guard (epic:enforcement-surface): close refuses when the engaged story is still
non-terminal AND commits were made since it was engaged — that is abandoning
shared work that never reached done. Pass --force to close anyway.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, workDir := resolveWorkContext(closeConfigArg)
			return runWorkClose(cmd.OutOrStdout(), repoRoot, workDir, resolveStateDB(closeConfigArg),
				args[0], resolveSession(closeSessionArg), closeForce, time.Now().UTC())
		},
	}
	closeCmd.Flags().StringVar(&closeConfigArg, "config", "", "Path to satellites.toml (defaults to walk-up from CWD).")
	closeCmd.Flags().StringVar(&closeSessionArg, "session", "", "Session key (default $CLAUDE_CODE_SESSION_ID, else 'local').")
	closeCmd.Flags().BoolVar(&closeForce, "force", false, "Close even when the story is non-terminal with commits made since engagement (overrides the ungated-work guard).")
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
	editable, _, _ := resolveEngageGuards(ctx, configPath, storyID, status)
	return editable
}

// resolveEngageGuards fetches the story once and derives the two engage-time
// guards from its embedded `## Workflow`: editability and the current state's
// actor. Best-effort like resolveEditable: any failure yields (true, "") so
// offline work is never over-blocked.
func resolveEngageGuards(ctx context.Context, configPath, storyID, status string) (bool, string, string) {
	req, err := json.Marshal(verb.DocumentGetRequest{ID: strings.TrimSpace(storyID)})
	if err != nil {
		return true, "", status
	}
	raw, err := dispatchVerb(ctx, "document_get", req, configPath, "")
	if err != nil {
		return true, "", status // offline / not resolvable → don't over-block
	}
	var resp verb.DocumentGetResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return true, "", status
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
		return true, "", status
	}
	return wf.IsEditable(status), wf.ActorOf(status), status
}

// driveTimeWorkflowGate runs satellites-workflow-review's deterministic reference
// dry-run over the workflow that governs the story being engaged, refusing the
// engagement when any reviewer_skill or [[wikilink]] resolves from no tier. This
// is the drive-time half of the gate (the upsert half runs at `workflow upsert`,
// sty_fc39ca77): the review runs BEFORE you start driving a workflow, so a
// dangling reference is caught and fixed rather than failing closed mid-drive.
//
// Best-effort and fail-open on infrastructure trouble: a story it cannot resolve,
// or an unreachable substrate (probed via the known-resident agent-goals
// principle), skips the gate rather than block every server-backed reference as a
// false dangling ref — the same "offline → don't over-block" stance
// resolveEngageGuards takes.
func driveTimeWorkflowGate(ctx context.Context, out io.Writer, configArg, userArg, storyID string) error {
	req, err := json.Marshal(verb.DocumentGetRequest{ID: strings.TrimSpace(storyID)})
	if err != nil {
		return nil
	}
	raw, err := dispatchVerb(ctx, "document_get", req, configArg, userArg)
	if err != nil {
		return nil // offline / not resolvable → don't over-block
	}
	var resp verb.DocumentGetResponse
	if json.Unmarshal(raw, &resp) != nil {
		return nil
	}
	storyBody := resp.RawBody
	if storyBody == "" && len(resp.Versions) > 0 {
		storyBody = resp.Versions[0].Body
	}

	// Resolve the workflow that ACTUALLY governs this story: the applies_to-matched
	// .satellites/workflows source, falling back to the story's embedded ## Workflow.
	govBody := storyBody
	sources := governingWorkflowSources(configArg)
	if _, name, ok := verb.ResolveGoverningWorkflow(resp.Document.Category, sources); ok {
		for _, s := range sources {
			if s.Name == name {
				govBody = s.Body
				break
			}
		}
	}

	resolveSkill, resolveDoc := workflowRefResolvers(ctx, configArg, userArg)
	// Connectivity probe: a known-resident system principle (the minimal product
	// contract per the constitution). If it does not resolve, the substrate is
	// unreachable — skip rather than flag every server-backed reference offline.
	if !resolveDoc("agent-goals") {
		return nil
	}
	findings := reviewWorkflowRefs(govBody, resolveSkill, resolveDoc)
	if len(findings) == 0 {
		return nil
	}
	fmt.Fprintf(out, "satellites-workflow-review BLOCKED engagement of %s — the governing workflow has %d unresolved reference(s):\n", storyID, len(findings))
	for _, f := range findings {
		fmt.Fprintf(out, "  ✗ %s\n", f.String())
	}
	return fmt.Errorf("work init: governing workflow has %d unresolved reference(s) — fix the workflow (satellites workflow validate) and re-engage", len(findings))
}

// refuseNonExecutorActor is the actor handoff stop (epic:graduated-workflow):
// an executor action in a state whose declared actor is not the executor is
// refused with whose turn it is. Empty actor = pre-actor semantics = allowed.
func refuseNonExecutorActor(action, storyID, status, actor string) error {
	if actor == "" || actor == "executor" {
		return nil
	}
	return fmt.Errorf("%s: story %s is in state %q whose actor is %q — it is %s's turn, not the executor's; not your state → stop",
		action, storyID, status, actor, actor)
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

// phaseIsTerminal reports whether `phase` is a terminal (finished) state of the
// repo's governing workflow(s) — engine-derived via workflow.IsTerminal, so a
// repo that RENAMES its terminal states is honoured with NO hardcoded names. The
// client holds no baked terminal-name list; it derives the answer from the
// substrate. `known` is false when no governing workflow resolves or `phase` is
// not a declared state, in which case the enforcement guards fail OPEN (never
// over-block), matching resolveEditable.
func phaseIsTerminal(configPath, phase string) (terminal bool, known bool) {
	phase = strings.TrimSpace(phase)
	if phase == "" {
		return false, false
	}
	for _, s := range governingWorkflowSources(configPath) {
		wf, err := workflow.Parse([]byte(s.Body))
		if err != nil || wf == nil {
			continue
		}
		if wf.HasState(phase) {
			return wf.IsTerminal(phase), true
		}
	}
	return false, false
}

// phaseOrUnset renders a phase for a message, naming an empty phase explicitly.
func phaseOrUnset(phase string) string {
	if strings.TrimSpace(phase) == "" {
		return "unset"
	}
	return phase
}

// commitsSince counts commits on HEAD authored since `since`, the signal for
// "work was committed during this engagement". It shells to git in repoRoot and
// fails OPEN (returns 0) on any error — not a git repo, no commits, git absent —
// so the enforcement guards never block on a git hiccup. A zero `since` returns
// 0 (no engagement time → nothing to compare).
func commitsSince(repoRoot string, since time.Time) int {
	if since.IsZero() {
		return 0
	}
	root := strings.TrimSpace(repoRoot)
	if root == "" {
		root = "."
	}
	cmd := exec.Command("git", "-C", root, "rev-list", "--count", "--since", since.UTC().Format(time.RFC3339), "HEAD")
	outBytes, err := cmd.Output()
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(outBytes)))
	if err != nil {
		return 0
	}
	return n
}

// runWorkClose ends a story's engagement for a session: a close event clears the
// store row, and engagement.json is removed when it names the story.
//
// Guard (sty_b713f886): refuse to close an engagement whose story is still
// non-terminal when commits were made since it was engaged — closing then
// abandons shared work that never reached done. force overrides. The guard
// fails OPEN (allows close) on any uncertainty (no engagement, git unavailable)
// so it never wedges a legitimate close.
func runWorkClose(out io.Writer, repoRoot, workDir, stateDB, storyID, session string, force bool, now time.Time) error {
	storyID = strings.TrimSpace(storyID)
	if storyID == "" {
		return fmt.Errorf("work close: story id required")
	}
	store, err := workstate.Open(stateDB)
	if err != nil {
		return fmt.Errorf("work close: open store: %w", err)
	}
	defer store.Close()

	if !force {
		if eng, ok, cErr := store.Current(session, storyID); cErr == nil && ok {
			// Engine-derived (no hardcoded terminal names): block only when the
			// phase is a KNOWN non-terminal state of the governing workflow.
			// Unresolvable/undeclared → fail open (allow close), never
			// over-blocking on a renamed terminal or an ungoverned repo.
			cfg := filepath.Join(repoRoot, ".satellites", "satellites.toml")
			if term, known := phaseIsTerminal(cfg, eng.Phase); known && !term {
				if n := commitsSince(repoRoot, eng.UpdatedAt); n > 0 {
					return fmt.Errorf("work close: %s is still %q (not terminal) and %d commit(s) were made since it was engaged — "+
						"closing now abandons shared work that never reached done. Drive it to done through its gates, or pass --force to close anyway",
						storyID, phaseOrUnset(eng.Phase), n)
				}
			}
		}
	}

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
