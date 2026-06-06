// `satellites story status_transition --skill <gate> <story-id>` — the
// client-side reviewer gate (sty_ffec5dab). The client holds NO workflow
// knowledge: it loads the story, runs the NAMED gate skill (--skill, required)
// against the story via `claude -p --append-system-prompt <gate-body>`, and
// reports the status the skill enacted. It does NOT resolve, read, or parse
// any workflow, and it does NOT compute a next_status. The story's `## Workflow`
// section (embedded in the story body by the executing agent) IS the workflow;
// the GATE skill reads it and derives its own target status.
//
// This is the one execution primitive: where the executing agent DOES the
// work, `status_transition` spawns the gate skill to JUDGE the transition
// (the former `story run` executor driver was retired in sty_6e1c3641 — the
// local agent is the executor). The gate skill enacts its own verdict — it
// patches the status (via the status_transition spine row) and writes the
// review_* rows. Those writes authenticate as the operator's own admin user
// (the server authorizes status_transition / review_* by the admin user behind
// the call), so there is no separately minted reviewer key. This command
// orchestrates (record that the gate was requested, dispatch it) and then
// reports the status the skill enacted; it does not patch the status itself.
//
// Single-source reuse: the gate subprocess + decision parse are owned by
// internal/verb (ClaudeCLIGateDispatcher). This command only orchestrates,
// and stays behind internal/verb's request/response types per the CLI
// layering guard (no internal/document, internal/ledger, or internal/workflow
// imports).

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workstate"
	"github.com/spf13/cobra"
)

// gateDispatchTimeout caps a single gate `claude -p` run. A done-review
// builds + runs the change's tests in the worktree (sty_cba1d47b granted
// it Bash); a Go test run that starts testcontainers/Postgres routinely
// exceeds five minutes, and a gate killed mid-verification cannot enact,
// so the story silently fails to advance. Fifteen minutes leaves headroom
// for a real build+test pass while still bounding a runaway gate.
const gateDispatchTimeout = 15 * time.Minute

// summariserTimeout caps the per-transition step summariser run that
// follows a gate decision (sty_2517f6b8). A summary reads the story + tree
// and emits prose — no build — so it is far shorter than the gate cap.
const summariserTimeout = 5 * time.Minute

// claimLeaseTTL is the lifetime of the local work-claim lease a gate run
// holds (store.ClaimWork). It MUST outlive the whole dispatch so a second
// reviewer cannot reclaim the work area mid-run while the gate is still
// building+testing and writing its post-decision spine + step_summary rows.
// Derived from both timeouts + headroom so the lease and the work it covers
// cannot silently drift apart.
const claimLeaseTTL = gateDispatchTimeout + summariserTimeout + 5*time.Minute

func newStoryReviewCmd(configArg, userArg *string) *cobra.Command {
	var (
		claudeBin    string
		worktreeRoot string
		skill        string
	)
	cmd := &cobra.Command{
		Use:   "status_transition --skill <gate> <story-id>",
		Short: "Run a named reviewer gate skill against a story, client-side",
		Long: `status_transition runs a named reviewer gate skill for one story on the
operator machine.

It resolves the story via server verbs and runs the gate named by --skill
(required) as ` + "`claude -p --append-system-prompt <gate-body>`" + ` against the worktree.
The client holds no workflow knowledge: the story's ` + "`## Workflow`" + ` section is
the workflow, and the GATE skill reads it to derive its own target status and
enact the transition (review_accept + status_transition ledger rows on accept;
the rejection notes on reject). The gate runs where the worktree and claude
live — the substrate stays on the server.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return runReview(ctx, reviewOpts{
				StoryID:      strings.TrimSpace(args[0]),
				ConfigPath:   *configArg,
				UserArg:      *userArg,
				ClaudeBin:    claudeBin,
				WorktreeRoot: worktreeRoot,
				Skill:        strings.TrimSpace(skill),
				Stdout:       cmd.OutOrStdout(),
				Stderr:       cmd.ErrOrStderr(),
			})
		},
	}
	cmd.Flags().StringVar(&claudeBin, "claude-bin", "", "Path to the claude binary (defaults to $SATELLITES_CLAUDE_BIN or `claude` on PATH).")
	cmd.Flags().StringVar(&worktreeRoot, "worktree", "", "Worktree root the gate runs against (default: current directory).")
	cmd.Flags().StringVar(&skill, "skill", "", "REQUIRED. Name the gate skill to run against the story. The gate reads the story's `## Workflow` to derive its own target status and enact the transition.")
	return cmd
}

type reviewOpts struct {
	StoryID      string
	ConfigPath   string
	UserArg      string
	ClaudeBin    string
	WorktreeRoot string
	// Skill is REQUIRED: the name of the gate skill to run against the story.
	// The client does not resolve a workflow or pick a transition — it
	// dispatches this gate and reports. The gate reads the story's `## Workflow`
	// to derive its own target status and enact the transition.
	Skill  string
	Stdout io.Writer
	Stderr io.Writer
}

// reviewStory is the minimal projection the reviewer needs from a
// document_get response. Typed inline so the CLI does not name
// internal/document (layering guard).
type reviewStory struct {
	ID          string
	Type        string
	Status      string
	Category    string
	ProjectID   string
	WorkspaceID string
}

func runReview(ctx context.Context, opts reviewOpts) error {
	if opts.StoryID == "" {
		return fmt.Errorf("story id required")
	}

	// 0. The gate to run is named explicitly — the client holds no workflow
	// knowledge and never resolves a gate from status. --skill is required.
	gateSkill := strings.TrimSpace(opts.Skill)
	if gateSkill == "" {
		return fmt.Errorf("--skill is required: name the gate skill to run")
	}

	// 1. Resolve the story (substrate read — server).
	story, body, err := reviewGetStory(ctx, opts)
	if err != nil {
		return err
	}

	// 1b. Open the per-repo working-state store (sty_676e070c) — the inbox +
	// claim rows that replaced the per-story .satellites/work/<story>/ files.
	store, err := workstate.Open(resolveStateDB(opts.ConfigPath))
	if err != nil {
		return fmt.Errorf("open work store: %w", err)
	}
	defer store.Close()

	// 1c. Claim the local work area (sty_8e8ec0e7) — lease under a store
	// transaction (the flock is retired; WAL + single-writer pool serialise
	// it). A second reviewer with a live foreign lease is refused, so two
	// reviewers cannot double-claim the same transition (AC3). A re-run by the
	// same operator reclaims its own lease.
	if err := store.ClaimWork(story.ID, workClaimant(resolveCallerUserID(opts.UserArg)), story.Status, claimLeaseTTL, time.Now()); err != nil {
		return fmt.Errorf("claim work area for %s: %w", story.ID, err)
	}

	// 2. Resolve only the optional step-summariser skill (a post-transition
	// project-config setting — NOT a workflow read). The client does not read,
	// resolve, or parse any workflow: the story's `## Workflow` is the workflow,
	// and the gate skill derives its own target from it.
	dispatch := func(ctx context.Context, name string, req json.RawMessage) (json.RawMessage, error) {
		return dispatchVerb(ctx, name, req, opts.ConfigPath, opts.UserArg)
	}
	summariserSkill := reviewStepSummariserSkill(ctx, dispatch, story)

	// 5. Recent ledger for gate context. Owned by recentGateLedger (shared
	// with `satellites context show` so the gate bundle is sized from the same
	// assembly it is fed): hot path serves from the local inbox when present —
	// no wire round-trip — and falls back to ledger_list otherwise, capped at 5.
	recentResp, recentSource := recentGateLedger(ctx, story.ID, opts.ConfigPath, opts.UserArg)
	recent := recentResp.Entries
	fmt.Fprintf(opts.Stdout, "recent context: %s (%d rows)\n", recentSource, len(recent))

	// 6. The gate's spine writes + status patch run under the operator's own
	// authenticated credential — no separately-minted reviewer key. The
	// server authorizes status_transition / review_* by the admin user behind
	// the call (see requireLedgerAppendRole), so the client's own auth is the
	// authority.

	// 6b. Spine: the gate was requested. Stage it to the local inbox
	// (sty_8e8ec0e7); the batched flush (step 10) writes it durably to the
	// server with its local_ref. The verdict rows stay the skill's to enact.
	// No to_status — the client does not know it; the gate derives it from the
	// story's `## Workflow`.
	reqPayload, _ := json.Marshal(map[string]any{"gate": gateSkill, "from_status": story.Status})
	if _, sErr := store.InboxAppend(story.ID, "review_requested",
		fmt.Sprintf("gate %s: from %s", gateSkill, story.Status), reqPayload, time.Now()); sErr != nil {
		fmt.Fprintf(opts.Stderr, "warn: stage review_requested to inbox: %v\n", sErr)
	}

	// 7. Run the gate locally (owned by internal/verb's dispatcher). The
	// dispatch timeout (gateDispatchTimeout) must cover a done-review that
	// actually builds + runs the change's tests; the work-claim lease
	// (claimLeaseTTL, above) is derived from it so the lease outlives the run
	// and a second reviewer cannot reclaim mid-build.
	disp := verb.ClaudeCLIGateDispatcher{BinaryPath: strings.TrimSpace(opts.ClaudeBin), DefaultTimeout: gateDispatchTimeout}
	gateOut, err := disp.Dispatch(ctx, verb.GateInput{
		SkillName:    gateSkill,
		StoryID:      story.ID,
		ProjectID:    story.ProjectID,
		WorkspaceID:  story.WorkspaceID,
		StoryBody:    body,
		StoryStatus:  story.Status,
		RecentLedger: recent,
		WorktreeRoot: opts.WorktreeRoot,
	})
	if err != nil {
		return fmt.Errorf("gate dispatch: %w", err)
	}
	fmt.Fprintf(opts.Stdout, "decision: %s\n", gateOut.Decision)
	if strings.TrimSpace(gateOut.Notes) != "" {
		fmt.Fprintf(opts.Stdout, "notes: %s\n", gateOut.Notes)
	}

	// 8. The reviewer skill enacts its own verdict (sty_db5cdef0). Running
	// under the operator's inherited admin auth, the gate skill writes the
	// review_accept/review_reject + status_transition spine rows and, on
	// accept, patches the story status toward the target it derived from the
	// story's `## Workflow`. The client no longer performs the status patch or
	// those spine writes — it requests the gate (step 6b) and reports;
	// enactment is configuration in the skill, not code here.
	//
	// Read the status back (operator's own key — a plain read) so the
	// operator sees whether the skill enacted. This read is observability,
	// NOT enactment: the client never patches the status itself.
	observed, _ := reviewObserveStatus(ctx, opts, story.ID)
	if observed != "" && observed != story.Status {
		fmt.Fprintf(opts.Stdout, "status: %s → %s\n", story.Status, observed)
	} else {
		fmt.Fprintf(opts.Stdout, "status: %s (unchanged)\n", story.Status)
		if gateOut.Decision == verb.GateDecisionAccept {
			// The gate accepted but the status did not advance — the skill
			// did not enact. Surface it; do not silently paper over it by
			// patching from the client (that is exactly what this story
			// moved into the skill).
			fmt.Fprintf(opts.Stderr,
				"warn: gate accepted but status is still %q — the reviewer skill did not enact its transition\n",
				story.Status)
		}
	}

	// 8b. Durable QA-evidence capture (sty_7d2e9847): record this gate run —
	// skill, decision, reject reasons, from/to status — to the store as a
	// durable, story-linked, queryable trail that survives the run. This closes
	// the spike's "gates run in-place with no logging" gap; it is read out of
	// band by the QA view + the order:9 audit, never injected into a turn. The
	// server ledger spine (review_accept/reject) is unchanged — this is the
	// local capture. Best-effort: a capture failure must not fail the review.
	if _, eErr := store.RecordEvidence(workstate.Evidence{
		Story:      story.ID,
		Kind:       workstate.EvidenceGate,
		Label:      gateSkill,
		Decision:   string(gateOut.Decision),
		Notes:      gateOut.Notes,
		FromStatus: story.Status,
		ToStatus:   observed,
		TS:         time.Now(),
	}); eErr != nil {
		fmt.Fprintf(opts.Stderr, "warn: capture gate evidence for %s: %v\n", story.ID, eErr)
	}

	// 9. Per-transition step summary (sty_2517f6b8). When project-config names
	// a step_summariser_skill, run it and record its prose as a step_summary
	// ledger row, tied to this transition. The to_status is the status the gate
	// enacted (read back at step 8) — the client does not compute it. Best-
	// effort: the transition has already happened, so a summariser failure warns
	// but does not fail the review. Inlined (not a helper) so the recent-ledger
	// slice flows by inference and the CLI never names internal/ledger.
	if summariserSkill != "" {
		toStatus := observed
		if toStatus == "" {
			toStatus = story.Status
		}
		summariser := verb.ClaudeCLISummariser{
			BinaryPath:     strings.TrimSpace(opts.ClaudeBin),
			DefaultTimeout: summariserTimeout,
		}
		summary, sErr := summariser.Summarise(ctx, verb.SummariserInput{
			SkillName:    summariserSkill,
			StoryID:      story.ID,
			ProjectID:    story.ProjectID,
			WorkspaceID:  story.WorkspaceID,
			StoryBody:    body,
			FromStatus:   story.Status,
			ToStatus:     toStatus,
			Decision:     gateOut.Decision,
			RecentLedger: recent,
			WorktreeRoot: opts.WorktreeRoot,
		})
		switch {
		case sErr != nil:
			fmt.Fprintf(opts.Stderr, "warn: step summariser %q failed: %v\n", summariserSkill, sErr)
		case strings.TrimSpace(summary) != "":
			sumPayload, _ := json.Marshal(map[string]any{"from_status": story.Status, "to_status": toStatus, "gate_skill": gateSkill, "decision": gateOut.Decision})
			if _, aErr := store.InboxAppend(story.ID, "step_summary", summary, sumPayload, time.Now()); aErr != nil {
				fmt.Fprintf(opts.Stderr, "warn: stage step_summary to inbox: %v\n", aErr)
			} else {
				fmt.Fprintf(opts.Stdout, "step summary recorded (%d chars)\n", len(summary))
			}
		}
	}

	// 10. Batched flush (sty_8e8ec0e7): write the client-owned signals staged
	// in the inbox this run (seq > last flushed) to the server ledger in a
	// single pass under the operator's own credential, each carrying its
	// local_ref. The verdict rows are NOT here — the gate skill enacts those
	// (sty_db5cdef0).
	flushLocalInbox(ctx, opts, store, story)

	// 11. Terminal cleanup (sty_676e070c): once the story reaches its terminal
	// status (`done`), its working-state rows have nothing more to flush — drop
	// them so a completed story leaves no residue in the store. The client
	// holds no workflow knowledge, so it keys on the canonical terminal status
	// the fix/feature/parent/urgent workflows share.
	if observed == "done" {
		if err := store.CleanupWork(story.ID); err != nil {
			fmt.Fprintf(opts.Stderr, "warn: cleanup work store for %s: %v\n", story.ID, err)
		}
	}
	return nil
}

// flushLocalInbox writes the inbox messages staged this run (seq above the
// last flushed seq recorded in the store's work_claim.last_seq) to the server
// ledger via the operator's own credential, each carrying local_ref:<seq> in
// its payload, then advances the high-water mark. Idempotent across iterative
// review runs — a row is flushed once. Best-effort: a flush failure warns; the
// server ledger remains the authority and a later run re-flushes from the
// persisted inbox.
func flushLocalInbox(ctx context.Context, opts reviewOpts, store *workstate.Store, story reviewStory) {
	claim, err := store.ReadWorkClaim(story.ID)
	if err != nil {
		fmt.Fprintf(opts.Stderr, "warn: read work claim %s: %v\n", story.ID, err)
		return
	}
	msgs, err := store.InboxReadAll(story.ID)
	if err != nil {
		fmt.Fprintf(opts.Stderr, "warn: read inbox %s: %v\n", story.ID, err)
		return
	}
	flushed, maxSeq := 0, claim.LastSeq
	for _, m := range msgs {
		if m.Seq <= claim.LastSeq {
			continue
		}
		payload := map[string]any{"local_ref": m.Seq}
		if len(m.Payload) > 0 {
			var orig map[string]any
			if json.Unmarshal(m.Payload, &orig) == nil {
				for k, v := range orig {
					payload[k] = v
				}
			}
		}
		reviewAppendLedger(ctx, opts, story, m.Kind, m.Body, payload)
		flushed++
		if m.Seq > maxSeq {
			maxSeq = m.Seq
		}
	}
	if flushed > 0 {
		if err := store.AdvanceInboxFlush(story.ID, maxSeq); err != nil {
			fmt.Fprintf(opts.Stderr, "warn: advance flush mark %s: %v\n", story.ID, err)
		}
		fmt.Fprintf(opts.Stdout, "flushed %d local inbox row(s) to ledger\n", flushed)
	}
}

// recentGateLedger assembles the ≤5-row recent-ledger slice a gate run
// receives — the hot path serves from the local inbox when present (no wire
// round-trip), falling back to ledger_list over the server otherwise. Either
// source unmarshals into the same response shape, so the CLI never names
// internal/ledger (layering guard); the inbox JSON mirrors the ledger Entry
// fields. Single source for both the reviewer gate (runReview) and the
// delivered-context view (`satellites context show`), so the bundle that view
// sizes is byte-identical to the one a gate is fed (AC2).
func recentGateLedger(ctx context.Context, storyID, configPath, userArg string) (verb.LedgerListResponse, string) {
	var recentResp verb.LedgerListResponse
	source := "ledger_list"
	// Serve the hot path from the store-backed inbox when present (sty_676e070c);
	// an unopenable store (or empty inbox) simply falls through to ledger_list.
	store, sErr := workstate.Open(resolveStateDB(configPath))
	if store != nil {
		defer store.Close()
	}
	localRaw, ok := []byte(nil), false
	if sErr == nil {
		localRaw, ok, _ = localRecentLedgerJSON(store, storyID, 5)
	}
	if ok {
		_ = json.Unmarshal(localRaw, &recentResp)
		source = "local inbox"
	} else if llReq, mErr := json.Marshal(verb.LedgerListRequest{StoryID: storyID}); mErr == nil {
		if raw, lErr := dispatchVerb(ctx, "ledger_list", llReq, configPath, userArg); lErr == nil {
			_ = json.Unmarshal(raw, &recentResp)
		}
	}
	if len(recentResp.Entries) > 5 {
		recentResp.Entries = recentResp.Entries[len(recentResp.Entries)-5:]
	}
	return recentResp, source
}

// reviewObserveStatus re-reads the story's current status for reporting.
// It uses the operator's stored key (a plain read) and never writes — it
// only lets the client show whether the skill enacted. Errors are
// non-fatal: an unreadable status just prints nothing.
func reviewObserveStatus(ctx context.Context, opts reviewOpts, storyID string) (string, error) {
	req, err := json.Marshal(verb.DocumentGetRequest{ID: storyID})
	if err != nil {
		return "", err
	}
	raw, err := dispatchVerb(ctx, "document_get", req, opts.ConfigPath, opts.UserArg)
	if err != nil {
		return "", err
	}
	var resp verb.DocumentGetResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", err
	}
	return resp.Document.Status, nil
}

func reviewGetStory(ctx context.Context, opts reviewOpts) (reviewStory, string, error) {
	req, err := json.Marshal(verb.DocumentGetRequest{ID: opts.StoryID})
	if err != nil {
		return reviewStory{}, "", err
	}
	raw, err := dispatchVerb(ctx, "document_get", req, opts.ConfigPath, opts.UserArg)
	if err != nil {
		return reviewStory{}, "", fmt.Errorf("resolve story: %w", err)
	}
	var resp verb.DocumentGetResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return reviewStory{}, "", fmt.Errorf("decode story: %w", err)
	}
	if resp.Document.Type != "story" {
		return reviewStory{}, "", fmt.Errorf("document %s is type=%q; review requires a story", opts.StoryID, resp.Document.Type)
	}
	body := resp.RawBody
	if body == "" && len(resp.Versions) > 0 {
		body = resp.Versions[0].Body
	}
	return reviewStory{
		ID:          resp.Document.ID,
		Type:        resp.Document.Type,
		Status:      resp.Document.Status,
		Category:    resp.Document.Category,
		ProjectID:   resp.Document.ProjectID,
		WorkspaceID: resp.Document.WorkspaceID,
	}, body, nil
}

// reviewStepSummariserSkill returns the project's optional step-summariser
// skill name (empty when unconfigured). It reads the slimmed project-config
// document — a non-dispatch, post-transition setting the index does not carry;
// a missing document or field simply disables per-transition summaries, so it
// never blocks the gate.
func reviewStepSummariserSkill(ctx context.Context, dispatch verbDispatch, story reviewStory) string {
	req, err := json.Marshal(verb.DocumentGetRequest{
		Scope:       "project",
		Name:        "project-config",
		WorkspaceID: story.WorkspaceID,
		ProjectID:   story.ProjectID,
		// Resolve through the cascade so a user override of project-config
		// (e.g. the step summariser) is honoured for that caller (sty_cbeeb452).
		Inherit: true,
	})
	if err != nil {
		return ""
	}
	raw, err := dispatch(ctx, "document_get", req)
	if err != nil {
		return ""
	}
	var resp verb.DocumentGetResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return ""
	}
	body := resp.RawBody
	if body == "" && len(resp.Versions) > 0 {
		body = resp.Versions[0].Body
	}
	cfg, err := verb.ParseProjectConfig(body)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cfg.StepSummariserSkill)
}

func reviewAppendLedger(ctx context.Context, opts reviewOpts, story reviewStory, kind, body string, payload map[string]any) {
	var pb json.RawMessage
	if len(payload) > 0 {
		if b, err := json.Marshal(payload); err == nil {
			pb = b
		}
	}
	req, err := json.Marshal(verb.LedgerAppendRequest{
		StoryID:     story.ID,
		ProjectID:   story.ProjectID,
		WorkspaceID: story.WorkspaceID,
		Kind:        kind,
		Body:        body,
		Payload:     pb,
	})
	if err != nil {
		return
	}
	if _, err := dispatchVerb(ctx, "ledger_append", req, opts.ConfigPath, opts.UserArg); err != nil {
		fmt.Fprintf(opts.Stderr, "ledger append %s failed: %v\n", kind, err)
	}
}
