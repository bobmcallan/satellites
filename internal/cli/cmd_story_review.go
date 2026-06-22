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
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/cliconfig"
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
		checkpoint   bool
		explain      bool
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
live — the substrate stays on the server.

An ungated ` + "`trigger: checkpoint`" + ` edge carries no reviewer gate — it is the
executor's DELIBERATE move, advanced with --checkpoint (no --skill). The
checkpoint is never a silent side-effect of a gate request (sty_21d2c535): run
` + "`status_transition --checkpoint <story-id>`" + ` to enact it.`,
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
				Checkpoint:   checkpoint,
				Explain:      explain,
				Stdout:       cmd.OutOrStdout(),
				Stderr:       cmd.ErrOrStderr(),
			})
		},
	}
	cmd.Flags().StringVar(&claudeBin, "claude-bin", "", "Path to the claude binary (defaults to $SATELLITES_CLAUDE_BIN or `claude` on PATH).")
	cmd.Flags().StringVar(&worktreeRoot, "worktree", "", "Worktree root the gate runs against (default: current directory).")
	cmd.Flags().StringVar(&skill, "skill", "", "Name the gate skill to run against the story (required unless --checkpoint). The gate reads the story's `## Workflow` to derive its own target status and enact the transition.")
	cmd.Flags().BoolVar(&checkpoint, "checkpoint", false, "Advance the current state's ungated `trigger: checkpoint` edge — a deliberate executor move that runs no gate. Mutually exclusive with --skill.")
	cmd.Flags().BoolVar(&explain, "explain", false, "Dry-run: resolve the governing workflow exactly as enactment would and report the outgoing edges + whether --skill <gate> can enact one — runs no gate and mutates nothing.")
	return cmd
}

type reviewOpts struct {
	StoryID      string
	ConfigPath   string
	UserArg      string
	ClaudeBin    string
	WorktreeRoot string
	// Skill names the gate skill to run against the story (required unless
	// Checkpoint). The client does not resolve a workflow or pick a transition —
	// it dispatches this gate and reports. The gate reads the story's
	// `## Workflow` to derive its own target status and enact the transition.
	Skill string
	// Checkpoint requests the executor's deliberate advance of the current
	// state's ungated `trigger: checkpoint` edge — no gate runs. Mutually
	// exclusive with Skill (sty_21d2c535).
	Checkpoint bool
	// Explain requests a read-only dry-run: resolve the governing workflow and
	// report the edges + the named gate's enactability, run no gate, mutate
	// nothing (sty_550e9595).
	Explain bool
	Stdout  io.Writer
	Stderr  io.Writer
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
	Tags        []string
}

// enactMismatch returns a LOUD error (sty_7f8f2e11) when a gate ACCEPTED but the
// client enacted no transition — the gate judged but the governing workflow
// declares no edge from this status for it (e.g. a story gate run on a task, or a
// gate run at a v2 state it does not govern). It names the workflow + status +
// gate so the wiring mismatch is obvious. Returns nil when the no-change is
// expected: a reject (or non-accept), or an accept the client DID enact
// (clientEnacted — a v2 or v1 edge), or an out-of-band run (a non-lifecycle gate
// at a v2 state, which only records its verdict). Pure — unit-testable.
func enactMismatch(decision string, clientEnacted, outOfBand bool, gate, workflow, status string) error {
	if decision != verb.GateDecisionAccept {
		return nil // reject (or non-accept) — leaving status put is correct
	}
	if clientEnacted {
		return nil // the client enacted the edge (v2 or v1) — success
	}
	if outOfBand {
		return nil // a non-lifecycle gate at a v2 state only records its verdict
	}
	wf := strings.TrimSpace(workflow)
	if wf == "" {
		wf = "(category default)"
	}
	return fmt.Errorf("status_transition: gate %q accepted but no edge from status=%q enacted in workflow %q — the gate JUDGED but the workflow declares no edge from %q for this gate (a different reviewer may govern it, or the gate was run on the wrong artifact). Run `satellites story status_transition <story> --skill %s --explain` to see which gate governs which edge",
		gate, status, wf, status, gate)
}

// syncStampVersionRe pulls the version out of a `<!-- satellites-sync:begin
// {... "version": N ...} satellites-sync:end -->` stamp on a materialised /
// embedded workflow source. Local hand-authored files carry no stamp.
var syncStampVersionRe = regexp.MustCompile(`satellites-sync:begin[\s\S]*?"version"\s*:\s*(\d+)`)

// syncStampVersion returns `vN` when body carries a sync stamp, else "-".
func syncStampVersion(body string) string {
	if m := syncStampVersionRe.FindStringSubmatch(body); m != nil {
		return "v" + m[1]
	}
	return "-"
}

// workflowSourceProvenance reports which tier (local / skill / embed) the named
// governing workflow resolved from and its stamp version — the local-vs-
// materialised provenance an agent needs when a transition surprises it
// (sty_550e9595). Mirrors the precedence of governingWorkflowSources.
func workflowSourceProvenance(name, configPath string) (tier, version string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "-", "-"
	}
	find := func(srcs []verb.WorkflowSource) (string, bool) {
		for _, s := range srcs {
			if strings.EqualFold(strings.TrimSpace(s.Name), name) {
				return s.Body, true
			}
		}
		return "", false
	}
	if b, ok := find(clientWorkflowSources(configPath)); ok {
		return "local", syncStampVersion(b)
	}
	if b, ok := find(materialisedWorkflowSources()); ok {
		return "skill", syncStampVersion(b)
	}
	if b, ok := find(embeddedWorkflowSources()); ok {
		return "embed", syncStampVersion(b)
	}
	return "unknown", "-"
}

// runExplain renders the `--explain` dry-run: the governing workflow as
// enactment resolves it (name + source tier + version), the outgoing edges from
// the current status with how each enacts, and the named gate's enactability
// verdict. Pure read — no claim, no gate, no mutation (sty_550e9595).
func runExplain(opts reviewOpts, story reviewStory, body string) error {
	sources := governingWorkflowSources(opts.ConfigPath)
	selector := verb.WorkflowSelector(story.Tags)
	res := verb.ExplainTransition(selector, body, story.Status, story.Category, opts.Skill, sources)

	out := opts.Stdout
	tier, ver := workflowSourceProvenance(res.Governing, opts.ConfigPath)
	gov := res.Governing
	if gov == "" {
		gov = "(none resolved)"
	}
	fmt.Fprintf(out, "explain %s\n", story.ID)
	fmt.Fprintf(out, "  workflow:  %s   source: %s   version: %s\n", gov, tier, ver)
	if res.Drift != "" {
		fmt.Fprintf(out, "  drift:     %s\n", res.Drift)
	}
	actor := res.Actor
	if actor == "" {
		actor = "-"
	}
	fmt.Fprintf(out, "  status:    %s  (actor: %s)\n", res.Status, actor)
	if len(res.Edges) == 0 {
		fmt.Fprintln(out, "  edges:     (none — terminal)")
	} else {
		fmt.Fprintf(out, "  edges from %s:\n", res.Status)
		for _, e := range res.Edges {
			drive := "ungated"
			switch {
			case e.Gate != "":
				drive = "--skill " + e.Gate
			case e.Trigger == "checkpoint":
				drive = "--checkpoint"
			}
			on := ""
			if e.On != "" {
				on = "  on:" + e.On
			}
			fmt.Fprintf(out, "    → %-12s %-44s [%s]%s\n", e.To, drive, e.Model, on)
		}
	}
	if res.Gate != "" {
		fmt.Fprintf(out, "  gate %s:\n    %s\n", res.Gate, res.Verdict)
	} else {
		fmt.Fprintf(out, "  %s\n", res.Verdict)
	}
	return nil
}

// checkpointDecision is the single rule behind --checkpoint (sty_21d2c535): the
// ungated `trigger: checkpoint` edge is the executor's DELIBERATE move, never a
// silent side-effect of a gate request. Given whether --checkpoint was asked for
// and whether the current state is a pure ungated-checkpoint state, it decides
// whether to enact the hop, or returns the error that steers the caller to the
// right action. Pure (no IO) so the four-quadrant contract is unit-pinnable.
//
//   - checkpoint state + --checkpoint → enact the hop.
//   - checkpoint state + --skill      → error: the state has no gate; use --checkpoint.
//   - other state     + --checkpoint → error: nothing to checkpoint here.
//   - other state     + --skill      → no checkpoint; proceed to the gate path.
func checkpointDecision(wantCheckpoint, isCheckpointState bool, status, cpTo, gateSkill, edgesHint string) (enact bool, err error) {
	switch {
	case isCheckpointState && wantCheckpoint:
		return true, nil
	case isCheckpointState && !wantCheckpoint:
		return false, fmt.Errorf("status_transition: state %q has no reviewer gate — its only transition is the ungated checkpoint %q → %q; advance it with --checkpoint (a deliberate executor move), not --skill %q",
			status, status, cpTo, gateSkill)
	case !isCheckpointState && wantCheckpoint:
		return false, fmt.Errorf("status_transition: --checkpoint given but state %q has no single ungated checkpoint edge to advance%s", status, edgesHint)
	default:
		return false, nil
	}
}

func runReview(ctx context.Context, opts reviewOpts) error {
	if opts.StoryID == "" {
		return fmt.Errorf("story id required")
	}

	// 0. The action is named explicitly — the client holds no workflow knowledge
	// and never resolves a gate from status. --skill names a reviewer gate;
	// --checkpoint advances an ungated `trigger: checkpoint` edge (the executor's
	// deliberate move). Exactly one applies (sty_21d2c535): a checkpoint must
	// never be a silent side-effect of naming a gate.
	// --explain is a read-only dry-run, so neither --skill nor --checkpoint is
	// required (a bare `--explain` shows the edges from the current status). The
	// action-naming contract below applies only to a real transition.
	gateSkill := strings.TrimSpace(opts.Skill)
	if !opts.Explain {
		switch {
		case opts.Checkpoint && gateSkill != "":
			return fmt.Errorf("--checkpoint advances the ungated checkpoint edge and runs no gate — pass --checkpoint OR --skill, not both")
		case !opts.Checkpoint && gateSkill == "":
			return fmt.Errorf("--skill is required: name the gate skill to run (or pass --checkpoint to advance an ungated checkpoint edge)")
		}
	}

	// 1. Resolve the story (substrate read — server).
	story, body, err := reviewGetStory(ctx, opts)
	if err != nil {
		return err
	}

	// --explain: a read-only dry-run. Resolve the governing workflow exactly as
	// enactment does and report the edges + the named gate's enactability, then
	// return — no claim, no gate, no mutation, no drift self-heal (sty_550e9595).
	if opts.Explain {
		return runExplain(opts, story, body)
	}

	// 1a. v2 edge resolution (epic:graduated-workflow). On a state whose
	// outgoing edges carry on:pass|fail the gate skill judges ONLY and this
	// client enacts the decision's edge deterministically — including bounded
	// fail loops and exhaustion. The resolution itself lives in internal/verb
	// (this file's layering guard: no internal/workflow import). A story with
	// no v2 edges from its current status follows the legacy path untouched.
	//
	// The gate trusts the RESOLVED governing workflow (sty_0889de7a): the edges
	// come from the workflow whose `applies_to` covers this story's category in
	// the materialised (scope-cascaded) set, NOT the story's embedded `##
	// Workflow` copy — so an embedded copy cannot weaken the gate. A divergent
	// embedded copy is surfaced as drift, not honoured.
	wfSources := governingWorkflowSources(opts.ConfigPath)
	// The story may RECORD its governing workflow BY NAME — a `workflow:<name>`
	// tag (sty_cfbcc6e2). When set it is the authority: the engine loads that
	// named workflow's edges, never the embedded ## Workflow. Fail closed up
	// front on a dangling or non-matching selector so the gate never enacts a
	// guessed workflow (the plan gate's reject is the sibling guard).
	selector := verb.WorkflowSelector(story.Tags)
	if selector != "" {
		if _, covers, ok := verb.ResolveByName(selector, story.Category, wfSources); !ok {
			return fmt.Errorf("status_transition: story %s names workflow selector %q which resolves to no workflow in the source set — pick a valid one (satellites workflow list %s) and re-tag", story.ID, selector, story.ID)
		} else if !covers {
			return fmt.Errorf("status_transition: story %s names workflow selector %q whose applies_to does not cover category %q — pick a matching workflow (satellites workflow list %s)", story.ID, selector, story.Category, story.ID)
		}
	}
	edges, governing, drift := verb.GoverningReconcile(selector, body, story.Status, story.Category, wfSources)
	if drift != "" {
		// The embedded `## Workflow` diverged from the authoritative governing
		// workflow — a governing-config edit staled this in-flight story's copy.
		// Self-heal: re-stamp the embed from the governing definition so every
		// embed reader (the entry gates' to_status resolution, the recovery edge,
		// the portal) reads the authoritative shape — there is one authoritative
		// source and no false "diverges … not honoured" warning. Fail-soft: a
		// restamp failure falls back to surfacing the drift and never fails the
		// transition.
		if synced, changed, gov, ok, rErr := reembedGoverningWorkflow(ctx, story, body, opts.ConfigPath, opts.UserArg, wfSources); ok && rErr == nil && changed {
			body = synced
			edges, governing, _ = verb.GoverningReconcile(selector, body, story.Status, story.Category, wfSources)
			fmt.Fprintf(opts.Stdout, "re-synced embedded ## Workflow from governing workflow %q\n", gov)
		} else {
			fmt.Fprintf(opts.Stderr, "workflow drift: %s\n", drift)
			if rErr != nil {
				fmt.Fprintf(opts.Stderr, "warn: re-sync embedded ## Workflow failed: %v\n", rErr)
			}
		}
	}
	_ = governing

	// v2 enactment authorisation (sty_26c94ca5): a v2 state's pass/fail edge is
	// enacted ONLY by the gate the workflow declares for it (edges.ReviewerSkill),
	// or by the state's own command (an actor:satellites state names no reviewer →
	// GateMatches is true). Any other gate run at a v2 state runs OUT OF BAND — it
	// judges and records a verdict + QA evidence, but enacts no lifecycle edge —
	// so a laxer/commit gate cannot stand in for the workflow's required gate and
	// silently advance the story. enactV2 guards every v2-enactment branch below.
	enactV2 := edges.IsV2 && edges.GateMatches(gateSkill)
	if edges.IsV2 && !enactV2 {
		fmt.Fprintf(opts.Stdout,
			"out-of-band: %q is not the lifecycle gate for state %q (its gate is %q) — verdict recorded, no transition\n",
			gateSkill, story.Status, edges.ReviewerSkill)
	}

	// Actor handoff stop: an operator state takes no review dispatch at all —
	// it is a human decision point, and the executor's move is to stop. THE
	// EXCEPTION (sty_0c98760e): when the story's workflow authorizes the gate
	// being run to leave this state via an unconditional reviewer_skill edge
	// (the recovery shape, {from: blocked, to: in_progress, reviewer_skill:
	// satellites-loop-recovery-review}), that named reviewer gate IS the
	// sanctioned path out — so let it dispatch. Any other gate on an operator
	// state still stops. Without this, a fail-loop-blocked story has no CLI exit
	// and the recovery gate (its reason to exist) can never run.
	if edges.Actor == "operator" {
		if _, ok := verb.GoverningGatedEdge(selector, body, story.Status, story.Category, gateSkill, wfSources); !ok {
			return fmt.Errorf("status_transition: story %s is in state %q whose actor is %q — it is the operator's turn; not your state → stop",
				story.ID, story.Status, edges.Actor)
		}
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

	// 1a'. Checkpoint-trigger traverse (epic:graduated-workflow story 6): a
	// state whose single ungated edge carries `trigger: checkpoint` is left by
	// the executor's checkpoint, not a review — the client enacts the move
	// deterministically (no gate, no judgment), then chains into the new
	// state's own semantics below (a satellites-actor state runs its command
	// and enacts pass/fail; a reviewer or operator state stops the traverse —
	// their move is a separate request / a human's turn).
	//
	// The checkpoint is a DELIBERATE executor move (--checkpoint), NOT a silent
	// side-effect of naming a gate (sty_21d2c535): before this fix, ANY
	// status_transition at a checkpoint state enacted the hop regardless of
	// --skill, so re-running the entry gate on an in_progress story silently
	// advanced it to shipping and skipped the executor's work. checkpointDecision
	// makes the contract explicit — the hop fires only under --checkpoint, and
	// naming --skill at a pure-checkpoint state errors rather than transitions.
	cpTo, cpOK := verb.GoverningCheckpoint(selector, body, story.Status, story.Category, wfSources)
	isCheckpointState := cpOK && !edges.IsV2 && edges.Actor != "operator"
	edgeHint := verb.GoverningEdgesHint(selector, body, story.Status, story.Category, wfSources)
	enactCheckpoint, cpErr := checkpointDecision(opts.Checkpoint, isCheckpointState, story.Status, cpTo, gateSkill, edgeHint)
	if cpErr != nil {
		return cpErr
	}
	if enactCheckpoint {
		reviewAppendLedger(ctx, opts, story, "status_transition",
			fmt.Sprintf("%s → %s (checkpoint)", story.Status, cpTo),
			map[string]any{"from_status": story.Status, "to_status": cpTo, "trigger": "checkpoint"})
		fmt.Fprintf(opts.Stdout, "checkpoint: %s → %s\n", story.Status, cpTo)
		story.Status = cpTo
		edges, _, _ = verb.GoverningReconcile(selector, body, story.Status, story.Category, wfSources)
		if edges.Actor != "satellites" {
			// Landed where someone else acts (reviewer gate = its own
			// request; operator = a human's turn; terminal = done).
			whose := edges.Actor
			if whose == "" {
				whose = "the next gate"
			}
			fmt.Fprintf(opts.Stdout, "status: now %s — %s's turn\n", story.Status, whose)
			flushLocalInbox(ctx, opts, store, story)
			return nil
		}
		// Landed on a satellites-actor state: fall through to run its command.
	}

	// KV iteration bound (sty_0c98760e): the operator can tune the fail-loop
	// bound as a key-value. `workflow.max_iterations`, resolved through the
	// variable cascade (project → workspace → system/global), overrides the
	// yaml `max_iterations` for this state's fail loop when set — applied once
	// here so BOTH the pre-dispatch guard below and verb.PlanV2Enactment honour
	// it. Unset (or unparseable) → the yaml value stands.
	if enactV2 && edges.MaxIterations > 0 {
		if kv := resolveWorkflowMaxIterations(ctx, opts, story); kv > 0 {
			edges.MaxIterations = kv
		}
	}

	// Pre-dispatch bound check: when the fail loop is bounded and the ledger
	// already carries the full quota of rejects for this state, the review is
	// not re-dispatched — the bound is spent. (Normally exhaustion was already
	// enacted at the Nth reject; this refusal covers the re-request race.)
	var priorRejects int
	if enactV2 && edges.MaxIterations > 0 {
		priorRejects = verb.CountEdgeRejects(fullLedgerViews(ctx, opts, story.ID), story.Status)
		if priorRejects >= edges.MaxIterations {
			return fmt.Errorf("status_transition: review for state %q refused — its fail loop is exhausted (%d/%d rejects); the story escalates to %q and it is not the executor's to re-request",
				story.Status, priorRejects, edges.MaxIterations, edges.OnExhausted)
		}
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

	// 7. Run the gate locally. Two judgment paths:
	//   - actor:satellites state → NO claude -p: the client runs the state's
	//     declared deterministic command; exit 0 selects the pass edge,
	//     non-zero the fail edge. No agent discretion anywhere in the loop.
	//   - otherwise → the claude gate skill judges (owned by internal/verb's
	//     dispatcher). The dispatch timeout (gateDispatchTimeout) must cover a
	//     done-review that actually builds + runs tests; the work-claim lease
	//     (claimLeaseTTL, above) is derived from it so the lease outlives the
	//     run and a second reviewer cannot reclaim mid-build.
	var gateOut verb.GateOutput
	if edges.Actor == "satellites" {
		if !edges.IsV2 {
			return fmt.Errorf("status_transition: state %q declares actor satellites but no on:pass|fail edges — nothing deterministic to enact", story.Status)
		}
		if edges.Command == "" {
			return fmt.Errorf("status_transition: state %q declares actor satellites but names no command — the client cannot advance it deterministically", story.Status)
		}
		gateOut = runSatellitesActorCommand(ctx, opts, edges.Command)
	} else {
		disp := verb.ClaudeCLIGateDispatcher{
			BinaryPath:     strings.TrimSpace(opts.ClaudeBin),
			Model:          reviewerModel(opts.ConfigPath),
			DefaultTimeout: gateDispatchTimeout,
			// Server-fetch fallback (sty_b8de4776): a non-embedded reviewer absent
			// from .claude/skills is fetched from the server and injected, so a
			// substrate reviewer needs no local install. The local dir stays the
			// first non-embedded source (offline cache); this is the fallback.
			Fetch: serverGateFetcher(opts.ConfigPath, opts.UserArg),
		}
		gateOut, err = disp.Dispatch(ctx, verb.GateInput{
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
	}
	fmt.Fprintf(opts.Stdout, "decision: %s\n", gateOut.Decision)
	if strings.TrimSpace(gateOut.Notes) != "" {
		fmt.Fprintf(opts.Stdout, "notes: %s\n", gateOut.Notes)
	}

	// 7b. Client enactment (epic:enactment-convergence): the gate JUDGED; the
	// CLIENT writes the transition. No gate writes its own status_transition —
	// legacy-self-enact is retired, so a judge-only gate can never stall a story.
	// Two client-enacted edge shapes, same gate-judges/client-enacts contract;
	// only the gate the workflow declares for the edge enacts — any other gate
	// ran out of band (records its verdict, moves nothing):
	//   v2 — on:pass/fail edges with bounded fail loops (PlanV2Enactment).
	//   v1 — an unconditional {from,to,reviewer_skill} edge: accept → to; a
	//        reject records its verdict and stays put.
	clientEnacted := false
	switch {
	case enactV2:
		plan, perr := verb.PlanV2Enactment(gateOut.Decision, gateSkill, story.Status, gateOut.Notes, edges, priorRejects)
		if perr != nil {
			return fmt.Errorf("v2 enactment: %w", perr)
		}
		for _, row := range plan.Rows {
			reviewAppendLedger(ctx, opts, story, row.Kind, row.Body, row.Payload)
		}
		clientEnacted = true
		if plan.Exhausted {
			fmt.Fprintf(opts.Stdout, "exhausted: fail loop bound reached — escalated to %s\n", plan.ToStatus)
		}
	case !edges.IsV2:
		// A v1 (unconditional) reviewer-gated edge for THIS gate, resolved from the
		// governing workflow so a reference-resolved workflow (baseline / task, no
		// embedded ## Workflow) still enacts and an embedded copy cannot weaken it.
		// The match is by gateSkill: a state may carry several gated edges to
		// different targets (e.g. backlog → in_progress vs backlog → cancelled).
		if v1To, ok := verb.GoverningGatedEdge(selector, body, story.Status, story.Category, gateSkill, wfSources); ok {
			switch gateOut.Decision {
			case verb.GateDecisionAccept:
				reviewAppendLedger(ctx, opts, story, "review_accept", gateOut.Notes,
					map[string]any{"from_status": story.Status, "to_status": v1To, "gate": gateSkill})
				reviewAppendLedger(ctx, opts, story, "status_transition", fmt.Sprintf("%s → %s", story.Status, v1To),
					map[string]any{"from_status": story.Status, "to_status": v1To, "gate": gateSkill})
				clientEnacted = true
			case verb.GateDecisionReject:
				reviewAppendLedger(ctx, opts, story, "review_reject", gateOut.Notes,
					map[string]any{"from_status": story.Status, "gate": gateSkill})
				clientEnacted = true
			}
		}
	}

	// 8. Read the status back (the client's own key — a plain read) so the
	// operator sees the transition the client enacted in step 7b. This read is
	// observability, NOT enactment: the client already wrote the status_transition
	// (the ledger_append projects it onto the story's status).
	observed, _ := reviewObserveStatus(ctx, opts, story.ID)
	if observed != "" && observed != story.Status {
		fmt.Fprintf(opts.Stdout, "status: %s → %s\n", story.Status, observed)
	} else {
		// LOUD FAIL (sty_7f8f2e11): a gate that ACCEPTED but the client enacted
		// nothing — and it was NOT a legitimate out-of-band run (a v2 state's
		// non-lifecycle gate) — is a workflow-wiring mismatch (e.g. a story gate
		// run on a task, or a gate that governs no edge from this status), not
		// success. Fail with a named diagnostic; never warn-and-succeed (the
		// earlier silent warn let a story gate "pass" against a task — the
		// 2026-06-22 VIRE log).
		if err := enactMismatch(gateOut.Decision, clientEnacted, edges.IsV2 && !enactV2, gateSkill, governing, story.Status); err != nil {
			return err
		}
		fmt.Fprintf(opts.Stdout, "status: %s (unchanged)\n", story.Status)
	}

	// 8a. Refresh the local engagement so the next edit isn't blocked by the
	// stale engage-time editability snapshot now that the reviewer advanced the
	// status (sty_2c232fa4). No-op when this session has no live engagement for
	// the story; best-effort (a refresh failure must not fail the review).
	if observed != "" && observed != story.Status {
		guards := resolveEngageGuards(ctx, opts.ConfigPath, story.ID, observed)
		if _, rErr := refreshEngagementPhase(store, resolveSession(""), story.ID, observed, guards.editable, guards.commitReady, time.Now()); rErr != nil {
			fmt.Fprintf(opts.Stderr, "warn: refresh engagement after transition: %v\n", rErr)
		}
		// Real-time activity (epic:dynamic-workflow-status, order:1): flush the
		// phase-refresh engagement event so the portal sees the transition's
		// activity immediately, not only on the next batch `work sync`.
		realtimeEmitFn(ctx, opts.ConfigPath)
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
	// Skipped on the satellites-actor path: that lane is deterministic
	// end-to-end — no claude -p anywhere in it, the summariser included.
	if edges.Actor == "satellites" {
		summariserSkill = ""
	}
	if summariserSkill != "" {
		toStatus := observed
		if toStatus == "" {
			toStatus = story.Status
		}
		summariser := verb.ClaudeCLISummariser{
			BinaryPath:     strings.TrimSpace(opts.ClaudeBin),
			Model:          reviewerModel(opts.ConfigPath),
			DefaultTimeout: summariserTimeout,
			// Server-fetch fallback (sty_b8de4776): a summariser skill absent from
			// .claude/skills resolves from the server, like a gate — so the
			// summariser needs no local install.
			Fetch: serverGateFetcher(opts.ConfigPath, opts.UserArg),
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

// resolveWorkflowMaxIterations returns the operator's KV override for the
// fail-loop bound, or 0 when unset/unparseable (caller keeps the yaml value).
// It reads `workflow.max_iterations` through the variable cascade rooted at the
// story's project scope (project → workspace → system/global). Best-effort: any
// dispatch or parse error yields 0.
func resolveWorkflowMaxIterations(ctx context.Context, opts reviewOpts, story reviewStory) int {
	if strings.TrimSpace(story.ProjectID) == "" || strings.TrimSpace(story.WorkspaceID) == "" {
		return 0
	}
	req, err := json.Marshal(map[string]any{
		"name":         "workflow.max_iterations",
		"scope":        "project",
		"project_id":   story.ProjectID,
		"workspace_id": story.WorkspaceID,
		"inherit":      true,
	})
	if err != nil {
		return 0
	}
	raw, err := dispatchVerb(ctx, "variable_get", req, opts.ConfigPath, opts.UserArg)
	if err != nil {
		return 0
	}
	return parseMaxIterations(raw)
}

// parseMaxIterations extracts a positive iteration bound from a variable_get
// response. It returns 0 — caller keeps the yaml value — for anything that is
// not a clean positive integer: a malformed response, an absent/empty value, a
// non-numeric value, or a value <= 0. This is the fallback safety property: a
// misconfigured KV never weakens or breaks the fail-loop bound.
func parseMaxIterations(raw json.RawMessage) int {
	var resp struct {
		Value string `json:"value"`
	}
	if json.Unmarshal(raw, &resp) != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(resp.Value))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// fullLedgerViews fetches the story's COMPLETE ledger (cursor-paginated,
// oldest-first) projected to the minimal view the v2 reject counter reads.
// Unlike the 5-row gate slice this must see every reject since the loop's
// last re-arm — a bound counted from a truncated window would never trip.
// Best-effort: an unreachable ledger yields an empty slice (count 0), which
// fails open to dispatching the review — the reviewer remains the gate.
func fullLedgerViews(ctx context.Context, opts reviewOpts, storyID string) []verb.LedgerEntryView {
	var out []verb.LedgerEntryView
	cursor := ""
	for {
		req, err := json.Marshal(verb.LedgerListRequest{StoryID: storyID, Limit: 200, Cursor: cursor})
		if err != nil {
			return out
		}
		raw, err := dispatchVerb(ctx, "ledger_list", req, opts.ConfigPath, opts.UserArg)
		if err != nil {
			return out
		}
		var resp verb.LedgerListResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return out
		}
		for _, e := range resp.Entries {
			out = append(out, verb.LedgerEntryView{Kind: e.Kind, Payload: e.Payload})
		}
		if resp.NextCursor == "" {
			return out
		}
		cursor = resp.NextCursor
	}
}

// reviewerModel resolves the reviewer lane's model from satellites.toml
// (reviewer_model, sty_c7a5d741). Empty — unset key or unloadable config —
// means no --model flag: the reviewer inherits the harness default.
func reviewerModel(configPath string) string {
	cfg, _, _ := cliconfig.Load(configPath)
	return strings.TrimSpace(cfg.ReviewerModel)
}

// runSatellitesActorCommand advances an actor:satellites state: it runs the
// state's declared command in the worktree and maps the exit code onto the
// gate decision — exit 0 = pass/accept, non-zero = fail/reject. The notes
// carry the command, its exit, and an output tail so the verdict row reads
// like evidence, but the decision is the exit code alone.
func runSatellitesActorCommand(ctx context.Context, opts reviewOpts, command string) verb.GateOutput {
	cctx, cancel := context.WithTimeout(ctx, gateDispatchTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "sh", "-c", command)
	if strings.TrimSpace(opts.WorktreeRoot) != "" {
		cmd.Dir = opts.WorktreeRoot
	}
	outBytes, runErr := cmd.CombinedOutput()
	tail := strings.TrimSpace(string(outBytes))
	const tailCap = 1000
	if len(tail) > tailCap {
		tail = "…" + tail[len(tail)-tailCap:]
	}
	if runErr == nil {
		return verb.GateOutput{Decision: verb.GateDecisionAccept,
			Notes: fmt.Sprintf("satellites-actor command %q exit 0\n%s", command, tail)}
	}
	return verb.GateOutput{Decision: verb.GateDecisionReject,
		Notes: fmt.Sprintf("satellites-actor command %q failed (%v)\n%s", command, runErr, tail)}
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
	// Stories and top-level tasks (epic:project-tasks) share this gate-dispatch
	// path: both resolve a governing workflow by category and are driven by a
	// reviewer that enacts a status_transition. The rest of the dispatch is
	// type-agnostic (it keys on the object id + category).
	if resp.Document.Type != "story" && resp.Document.Type != "task" {
		return reviewStory{}, "", fmt.Errorf("document %s is type=%q; review requires a story or task", opts.StoryID, resp.Document.Type)
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
		Tags:        resp.Document.Tags,
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
