// `satellites story review <story-id>` — the client-side reviewer gate
// (sty_ffec5dab). The gate is intrinsically client-side: it reads the
// workflow skill from the local worktree and runs `claude -p
// --append-system-prompt <gate-body>` against the tree under review. The substrate (story, project-config,
// ledger, status) stays server-side and is reached through the existing
// document_get / document_upsert / ledger_* verbs — no new server verb.
//
// This is the sibling of `story run` (the executor driver). Where `run`
// spawns claude to DO the work, `review` spawns the gate skill to JUDGE
// the transition. The gate skill enacts its own verdict — it patches the
// status and writes the spine rows under its minted reviewer key
// (sty_db5cdef0). This command orchestrates (mint key, pick transition,
// record that the gate was requested) and then reports the status the
// skill enacted; it does not patch the status itself.
//
// Single-source reuse: transition selection is owned by
// internal/workflow (Workflow.PickTransition); project-config parsing
// and the gate subprocess + decision parse are owned by internal/verb
// (ParseProjectConfig, ClaudeCLIGateDispatcher). This command only
// orchestrates them, and stays behind internal/verb's request/response
// types per the CLI layering guard (no internal/document or
// internal/ledger imports).

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

	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workflow"
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

// reviewerKeyTTL is the lifetime of the reviewer key minted for a gate
// run. It MUST outlive the whole dispatch so the gate can write its
// post-decision spine rows (review_accept/reject + status_transition) after
// a long build+test, AND the step summariser that runs after the decision
// can write its step_summary row under the same key — otherwise those
// writes 401 on an expired key and the ledger trail is lost (sty_64c6159f,
// sty_2517f6b8). Derived from both timeouts + headroom so they cannot
// silently drift apart.
const reviewerKeyTTL = gateDispatchTimeout + summariserTimeout + 5*time.Minute

func newStoryReviewCmd(configArg, userArg *string) *cobra.Command {
	var (
		claudeBin    string
		worktreeRoot string
	)
	cmd := &cobra.Command{
		Use:   "review <story-id>",
		Short: "Run the workflow gate for a story's current → next transition, client-side",
		Long: `review runs the reviewer gate for one story on the operator machine.

It resolves the story and project-config via server verbs, reads the
workflow skill from the LOCAL worktree, picks the gated transition, and
runs the gate as ` + "`claude -p --append-system-prompt <gate-body>`" + ` against the worktree.
On accept it advances the story status via document_upsert and records
review_accept + status_transition ledger rows; on reject it records the
rejection notes. The gate runs where the worktree and claude live — the
substrate stays on the server.`,
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
				Stdout:       cmd.OutOrStdout(),
				Stderr:       cmd.ErrOrStderr(),
			})
		},
	}
	cmd.Flags().StringVar(&claudeBin, "claude-bin", "", "Path to the claude binary (defaults to $SATELLITES_CLAUDE_BIN or `claude` on PATH).")
	cmd.Flags().StringVar(&worktreeRoot, "worktree", "", "Worktree root the workflow skill + gate run against (default: current directory).")
	return cmd
}

type reviewOpts struct {
	StoryID      string
	ConfigPath   string
	UserArg      string
	ClaudeBin    string
	WorktreeRoot string
	Stdout       io.Writer
	Stderr       io.Writer
	// ReviewerKey is the minted reviewer-role api-key the spine writes
	// (review_*, status_transition) and the status patch authenticate
	// with. Empty until mintReviewerKey runs; the reads above stay on the
	// operator's stored (executor) key (sty_e16f0553).
	ReviewerKey string
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

	// 1. Resolve the story (substrate read — server).
	story, body, err := reviewGetStory(ctx, opts)
	if err != nil {
		return err
	}
	storyType := strings.TrimSpace(story.Category)
	if storyType == "" {
		return fmt.Errorf("story %s has no category — workflow lookup needs a story type", story.ID)
	}

	// 1b. Claim the local work area (sty_8e8ec0e7) — flock + lease, atomic.
	// A second reviewer with a live foreign lease is refused, so two
	// reviewers cannot double-claim the same transition (AC5). A re-run by
	// the same operator reclaims its own lease.
	if err := claimWork(story.ID, workClaimant(resolveCallerUserID(opts.UserArg)), story.Status, reviewerKeyTTL, time.Now()); err != nil {
		return fmt.Errorf("claim work area for %s: %w", story.ID, err)
	}

	// 2. project-config → workflow skill path for this story type, plus the
	// optional step-summariser skill (server read).
	skillPath, summariserSkill, err := reviewWorkflowSkillPath(ctx, opts, story, storyType)
	if err != nil {
		return err
	}

	// 3. Read + parse the workflow skill from the LOCAL worktree.
	clean := filepath.Clean(skillPath)
	skillBytes, err := os.ReadFile(filepath.Join(opts.WorktreeRoot, clean))
	if err != nil {
		return fmt.Errorf("read workflow skill %q from worktree: %w", skillPath, err)
	}
	wf, err := workflow.Parse(skillBytes)
	if err != nil {
		return fmt.Errorf("parse workflow %q: %w", skillPath, err)
	}

	// 4. Pick the gated transition (owned by internal/workflow).
	transition, gateSkill, isDynamic, err := wf.PickTransition(story.Status)
	if err != nil {
		return err
	}

	// 5. Recent ledger for gate context. Hot path (sty_8e8ec0e7): serve from
	// the local inbox when present — no wire round-trip — and fall back to
	// ledger_list over the server otherwise. Either source unmarshals into
	// the same response shape, so the CLI never names internal/ledger
	// (layering guard); the inbox JSON mirrors the ledger Entry fields.
	var recentResp verb.LedgerListResponse
	recentSource := "ledger_list"
	if localRaw, ok, _ := localRecentLedgerJSON(story.ID, 5); ok {
		_ = json.Unmarshal(localRaw, &recentResp)
		recentSource = "local inbox"
	} else if llReq, mErr := json.Marshal(verb.LedgerListRequest{StoryID: story.ID}); mErr == nil {
		if raw, lErr := dispatchVerb(ctx, "ledger_list", llReq, opts.ConfigPath, opts.UserArg); lErr == nil {
			_ = json.Unmarshal(raw, &recentResp)
		}
	}
	recent := recentResp.Entries
	if len(recent) > 5 {
		recent = recent[len(recent)-5:]
	}
	fmt.Fprintf(opts.Stdout, "recent context: %s (%d rows)\n", recentSource, len(recent))

	// 6. Mint a short-lived reviewer key for the spine writes + status
	// patch. The operator's stored key is executor-role and the server
	// refuses review_*/status_transition rows and status patches from it;
	// reviewer minting is admin-gated (apikey_create), so an autonomous
	// non-admin executor cannot self-accept (sty_e16f0553). Fail before
	// the costly gate dispatch if the caller lacks reviewer authority.
	reviewerKey, reviewerKeyID, err := mintReviewerKey(ctx, opts, story)
	if err != nil {
		return fmt.Errorf("mint reviewer key for gate run (reviewer minting requires an admin user): %w", err)
	}
	opts.ReviewerKey = reviewerKey
	defer revokeReviewerKey(ctx, opts, reviewerKeyID)

	// 6b. Spine: the gate was requested. Stage it to the local inbox
	// (sty_8e8ec0e7); the batched flush (step 10) writes it durably to the
	// server with its local_ref. The verdict rows stay the skill's to enact.
	reqPayload, _ := json.Marshal(map[string]any{"gate": gateSkill, "from_status": story.Status, "to_status": transition.To, "dynamic": isDynamic})
	if _, sErr := inboxAppend(story.ID, "review_requested",
		fmt.Sprintf("gate %s: %s → %s", gateSkill, story.Status, transition.To), reqPayload, time.Now()); sErr != nil {
		fmt.Fprintf(opts.Stderr, "warn: stage review_requested to inbox: %v\n", sErr)
	}

	// 7. Run the gate locally (owned by internal/verb's dispatcher). The
	// dispatch timeout (gateDispatchTimeout) must cover a done-review that
	// actually builds + runs the change's tests; the minted reviewer key's
	// TTL (reviewerKeyTTL, above) is derived from it so the key outlives the
	// run and the gate's post-decision spine writes do not 401.
	disp := verb.ClaudeCLIGateDispatcher{BinaryPath: strings.TrimSpace(opts.ClaudeBin), DefaultTimeout: gateDispatchTimeout}
	gateOut, err := disp.Dispatch(ctx, verb.GateInput{
		SkillName:    gateSkill,
		StoryID:      story.ID,
		ProjectID:    story.ProjectID,
		WorkspaceID:  story.WorkspaceID,
		StoryBody:    body,
		StoryStatus:  story.Status,
		NextStatus:   transition.To,
		Dynamic:      isDynamic,
		RecentLedger: recent,
		WorktreeRoot: opts.WorktreeRoot,
		ReviewerKey:  opts.ReviewerKey,
	})
	if err != nil {
		return fmt.Errorf("gate dispatch: %w", err)
	}
	fmt.Fprintf(opts.Stdout, "decision: %s\n", gateOut.Decision)
	if strings.TrimSpace(gateOut.Notes) != "" {
		fmt.Fprintf(opts.Stdout, "notes: %s\n", gateOut.Notes)
	}

	// 8. The reviewer skill enacts its own verdict (sty_db5cdef0). Under
	// its minted reviewer key (SATELLITES_REVIEWER_API_KEY, passed in env),
	// the gate skill writes the review_accept/review_reject + status_transition
	// spine rows and, on accept, patches the story status toward the
	// workflow-declared target. The client no longer performs the status
	// patch or those spine writes — it requests the gate (step 6b) and
	// reports; enactment is configuration in the skill, not code here.
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

	// 9. Per-transition step summary (sty_2517f6b8). When project-config names
	// a step_summariser_skill, run it and record its prose as a step_summary
	// ledger row, tied to this transition. Best-effort: the transition has
	// already happened, so a summariser failure warns but does not fail the
	// review. Runs under the still-valid reviewer key (reviewerKeyTTL covers
	// gate + summariser). Inlined (not a helper) so the recent-ledger slice
	// flows by inference and the CLI never names internal/ledger.
	if summariserSkill != "" {
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
			ToStatus:     transition.To,
			Decision:     gateOut.Decision,
			RecentLedger: recent,
			ReviewerKey:  opts.ReviewerKey,
			WorktreeRoot: opts.WorktreeRoot,
		})
		switch {
		case sErr != nil:
			fmt.Fprintf(opts.Stderr, "warn: step summariser %q failed: %v\n", summariserSkill, sErr)
		case strings.TrimSpace(summary) != "":
			sumPayload, _ := json.Marshal(map[string]any{"from_status": story.Status, "to_status": transition.To, "gate_skill": gateSkill, "decision": gateOut.Decision})
			if _, aErr := inboxAppend(story.ID, "step_summary", summary, sumPayload, time.Now()); aErr != nil {
				fmt.Fprintf(opts.Stderr, "warn: stage step_summary to inbox: %v\n", aErr)
			} else {
				fmt.Fprintf(opts.Stdout, "step summary recorded (%d chars)\n", len(summary))
			}
		}
	}

	// 10. Batched flush (sty_8e8ec0e7): write the client-owned signals staged
	// in the inbox this run (seq > last flushed) to the server ledger in a
	// single pass under the reviewer key, each carrying its local_ref. The
	// verdict rows are NOT here — the gate skill enacts those (sty_db5cdef0).
	flushLocalInbox(ctx, opts, story)

	// 11. Reconcile + cleanup (AC6): the server ledger is authority. Once the
	// story reaches a terminal state (no outgoing transition), the local work
	// area has served its purpose — remove it. A non-terminal story keeps its
	// inbox so the next run serves recent context locally.
	if observed != "" && len(wf.TransitionsFrom(observed)) == 0 {
		if cErr := cleanupWork(story.ID); cErr != nil {
			fmt.Fprintf(opts.Stderr, "warn: cleanup work area %s: %v\n", story.ID, cErr)
		}
	}
	return nil
}

// flushLocalInbox writes the inbox messages staged this run (seq above the
// last flushed seq recorded in status.json) to the server ledger via the
// reviewer key, each carrying local_ref:<seq> in its payload, then advances
// the high-water mark. Idempotent across iterative review runs — a row is
// flushed once. Best-effort: a flush failure warns; the server ledger remains
// the authority and a later run re-flushes from the persisted inbox.
func flushLocalInbox(ctx context.Context, opts reviewOpts, story reviewStory) {
	st, err := readWorkStatus(story.ID)
	if err != nil {
		fmt.Fprintf(opts.Stderr, "warn: read work status %s: %v\n", story.ID, err)
		return
	}
	msgs, err := inboxReadAll(story.ID)
	if err != nil {
		fmt.Fprintf(opts.Stderr, "warn: read inbox %s: %v\n", story.ID, err)
		return
	}
	flushed, maxSeq := 0, st.LastSeq
	for _, m := range msgs {
		if m.Seq <= st.LastSeq {
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
		_ = withWorkLock(story.ID, func() error {
			cur, _ := readWorkStatus(story.ID)
			cur.StoryID = story.ID
			if maxSeq > cur.LastSeq {
				cur.LastSeq = maxSeq
			}
			return writeWorkStatus(story.ID, cur)
		})
		fmt.Fprintf(opts.Stdout, "flushed %d local inbox row(s) to ledger\n", flushed)
	}
}

// reviewObserveStatus re-reads the story's current status for reporting.
// It uses the operator's stored key (a plain read), never the reviewer
// key, and never writes — it only lets the client show whether the skill
// enacted. Errors are non-fatal: an unreadable status just prints nothing.
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

// reviewWorkflowSkillPath returns the workflow-skill path for the story type
// and the project's optional step-summariser skill name (empty when
// unconfigured) from a single project-config read.
func reviewWorkflowSkillPath(ctx context.Context, opts reviewOpts, story reviewStory, storyType string) (string, string, error) {
	req, err := json.Marshal(verb.DocumentGetRequest{
		Scope:       "project",
		Name:        "project-config",
		WorkspaceID: story.WorkspaceID,
		ProjectID:   story.ProjectID,
	})
	if err != nil {
		return "", "", err
	}
	raw, err := dispatchVerb(ctx, "document_get", req, opts.ConfigPath, opts.UserArg)
	if err != nil {
		return "", "", fmt.Errorf("load project-config: %w", err)
	}
	var resp verb.DocumentGetResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", "", fmt.Errorf("decode project-config: %w", err)
	}
	body := resp.RawBody
	if body == "" && len(resp.Versions) > 0 {
		body = resp.Versions[0].Body
	}
	cfg, err := verb.ParseProjectConfig(body)
	if err != nil {
		return "", "", fmt.Errorf("project-config: %w", err)
	}
	st, ok := cfg.StoryTypes[storyType]
	if !ok || strings.TrimSpace(st.WorkflowSkill) == "" {
		return "", "", fmt.Errorf("project-config has no workflow_skill for story_type=%q", storyType)
	}
	return st.WorkflowSkill, strings.TrimSpace(cfg.StepSummariserSkill), nil
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
	if _, err := dispatchVerbAs(ctx, "ledger_append", req, opts.ConfigPath, opts.UserArg, opts.ReviewerKey); err != nil {
		fmt.Fprintf(opts.Stderr, "ledger append %s failed: %v\n", kind, err)
	}
}

// mintReviewerKey mints a short-lived reviewer-role api-key for this gate
// run via apikey_create (under the operator's stored key). The verb
// admin-gates reviewer minting, so this fails for a non-admin caller —
// keeping the gate's authority real: an autonomous executor cannot mint
// a reviewer key and self-accept (sty_e16f0553).
func mintReviewerKey(ctx context.Context, opts reviewOpts, story reviewStory) (string, string, error) {
	req, err := json.Marshal(verb.APIKeyCreateRequest{
		Role:       "reviewer",
		ProjectID:  story.ProjectID,
		TTLSeconds: int(reviewerKeyTTL / time.Second), // outlive the gate dispatch (sty_64c6159f)
	})
	if err != nil {
		return "", "", err
	}
	raw, err := dispatchVerb(ctx, "apikey_create", req, opts.ConfigPath, opts.UserArg)
	if err != nil {
		return "", "", err
	}
	var resp verb.APIKeyCreateResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", "", fmt.Errorf("decode apikey_create: %w", err)
	}
	if strings.TrimSpace(resp.APIKey) == "" {
		return "", "", fmt.Errorf("apikey_create returned an empty key")
	}
	return resp.APIKey, resp.KeyID, nil
}

// revokeReviewerKey best-effort revokes the minted reviewer key once the
// gate run is done, so the elevated credential does not outlive the
// review. A revoke failure is logged, not fatal — the key is short-lived
// regardless (IssueReviewerKey).
func revokeReviewerKey(ctx context.Context, opts reviewOpts, keyID string) {
	if strings.TrimSpace(keyID) == "" {
		return
	}
	req, err := json.Marshal(verb.APIKeyRevokeRequest{KeyID: keyID})
	if err != nil {
		return
	}
	if _, err := dispatchVerb(ctx, "apikey_revoke", req, opts.ConfigPath, opts.UserArg); err != nil {
		fmt.Fprintf(opts.Stderr, "warn: revoke reviewer key %s failed: %v\n", keyID, err)
	}
}
