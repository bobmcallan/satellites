package verb

import (
	"encoding/json"
	"strings"
	"testing"
)

// v2StoryBody is a story body embedding the epic anchor's target model plus a
// command on the satellites state — the fixture every planner test reads.
const v2StoryBody = "# fixture\n\n## Workflow\n\n```yaml\n" +
	"states:\n" +
	"  - {name: in_progress,     actor: executor}\n" +
	"  - {name: techdebt-review, actor: satellites, command: \"true\"}\n" +
	"  - {name: done-review,     actor: reviewer}\n" +
	"  - {name: blocked,         actor: operator}\n" +
	"  - done\n" +
	"transitions:\n" +
	"  - {from: in_progress,     to: techdebt-review, trigger: checkpoint}\n" +
	"  - {from: techdebt-review, on: pass, to: done-review}\n" +
	"  - {from: techdebt-review, on: fail, to: in_progress, max_iterations: 3, on_exhausted: blocked}\n" +
	"  - {from: done-review,     on: pass, to: done}\n" +
	"  - {from: done-review,     on: fail, to: in_progress, max_iterations: 3, on_exhausted: blocked}\n" +
	"```\n"

func TestResolveV2Edges(t *testing.T) {
	e, err := ResolveV2Edges(v2StoryBody, "done-review")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !e.IsV2 || e.Actor != "reviewer" || e.PassTo != "done" || e.FailTo != "in_progress" ||
		e.MaxIterations != 3 || e.OnExhausted != "blocked" {
		t.Fatalf("edges = %+v", e)
	}
	sat, _ := ResolveV2Edges(v2StoryBody, "techdebt-review")
	if sat.Actor != "satellites" || sat.Command != "true" || !sat.IsV2 {
		t.Fatalf("satellites state edges = %+v", sat)
	}
	// The executor state has only a checkpoint edge — not v2-dispatchable.
	ex, _ := ResolveV2Edges(v2StoryBody, "in_progress")
	if ex.IsV2 {
		t.Fatalf("in_progress must not be IsV2: %+v", ex)
	}
	// Legacy story body → legacy path, no error.
	legacy, err := ResolveV2Edges("# t\n\n```yaml\nstates: [a, done]\ntransitions:\n  - {from: a, to: done, reviewer_skill: x}\n```\n", "a")
	if err != nil || legacy.IsV2 {
		t.Fatalf("legacy resolve = %+v err=%v", legacy, err)
	}
}

// gatedV2Body is a story whose v2 review state names a reviewer_skill on both
// its pass and fail edges — the well-formed review-state shape.
const gatedV2Body = "# fixture\n\n## Workflow\n\n```yaml\n" +
	"states:\n" +
	"  - {name: in_progress, actor: executor}\n" +
	"  - {name: gate-review, actor: reviewer}\n" +
	"  - {name: cmd-state,   actor: satellites, command: \"true\"}\n" +
	"  - done\n" +
	"transitions:\n" +
	"  - {from: in_progress, to: gate-review, trigger: checkpoint}\n" +
	"  - {from: gate-review, on: pass, to: done,        reviewer_skill: \"the-gate\"}\n" +
	"  - {from: gate-review, on: fail, to: in_progress, max_iterations: 3, on_exhausted: done, reviewer_skill: \"the-gate\"}\n" +
	"  - {from: cmd-state,   on: pass, to: done}\n" +
	"  - {from: cmd-state,   on: fail, to: in_progress, max_iterations: 3, on_exhausted: done}\n" +
	"```\n"

// TestEdgesCaptureReviewerSkill_AndGateMatches pins sty_26c94ca5: the v2 edge
// projection carries the declared reviewer_skill, and GateMatches authorises
// only that gate to enact — except a command-driven (no-reviewer) state, which
// any caller drives.
func TestEdgesCaptureReviewerSkill_AndGateMatches(t *testing.T) {
	e, _ := ResolveV2Edges(gatedV2Body, "gate-review")
	if !e.IsV2 || e.ReviewerSkill != "the-gate" {
		t.Fatalf("gate-review edges = %+v, want IsV2 with ReviewerSkill=the-gate", e)
	}
	// Only the declared gate enacts; a laxer/other gate does not (AC1).
	if !e.GateMatches("the-gate") {
		t.Fatal("the declared reviewer_skill must match")
	}
	if !e.GateMatches("THE-GATE") {
		t.Fatal("the match must be case-insensitive")
	}
	if e.GateMatches("satellites-intent-code-review") {
		t.Fatal("a non-matching gate must NOT be authorised to enact a v2 edge")
	}
	if e.GateMatches("") {
		t.Fatal("an empty gate must not match a named reviewer_skill")
	}
	// A command-driven satellites state names no reviewer → any caller drives it.
	cmd, _ := ResolveV2Edges(gatedV2Body, "cmd-state")
	if cmd.ReviewerSkill != "" || !cmd.GateMatches("anything") {
		t.Fatalf("command-driven state edges = %+v, want empty ReviewerSkill matching any caller", cmd)
	}
}

// TestGoverningGatedEdge pins v1 client-enact edge resolution (epic:enactment-
// convergence): an unconditional reviewer_skill edge (no on:/trigger) is the
// client-enacted edge for the gate it NAMES — matched by gate, resolved from the
// governing workflow (here the embedded-body fallback, since no source set covers
// the category). This replaces the retired RecoveryEdgeFrom: the operator-stop
// recovery edge (sty_0c98760e) is just one such v1 edge, now resolved uniformly.
func TestGoverningGatedEdge(t *testing.T) {
	// blocked with no unconditional gated edge → not resolvable, for any gate.
	if to, ok := GoverningGatedEdge("", v2StoryBody, "blocked", "", "satellites-loop-recovery-review", nil); ok {
		t.Fatalf("blocked without a gated edge must not resolve; got to=%q", to)
	}

	withRecovery := v2StoryBody[:len(v2StoryBody)-len("```\n")] +
		"  - {from: blocked, to: in_progress, reviewer_skill: \"satellites-loop-recovery-review\"}\n```\n"

	to, ok := GoverningGatedEdge("", withRecovery, "blocked", "", "satellites-loop-recovery-review", nil)
	if !ok || to != "in_progress" {
		t.Fatalf("gated edge should resolve blocked → in_progress for the named gate; got to=%q ok=%v", to, ok)
	}
	// A different gate does not name this edge — not resolved (it stops).
	if _, ok := GoverningGatedEdge("", withRecovery, "blocked", "", "satellites-story-done-review", nil); ok {
		t.Fatal("a non-matching gate must not resolve the gated edge")
	}
	// Empty skill never matches.
	if _, ok := GoverningGatedEdge("", withRecovery, "blocked", "", "", nil); ok {
		t.Fatal("empty skill must not match any edge")
	}
	// A pass/fail edge is conditional, not an unconditional gated edge.
	if _, ok := GoverningGatedEdge("", withRecovery, "done-review", "", "satellites-loop-recovery-review", nil); ok {
		t.Fatal("a conditional on:fail edge is not an unconditional gated edge")
	}
}

// TestPlanV2Enactment_FailEdgeLoop pins AC1: a reject enacts a real
// transition back to the executor state — visible as a status move.
func TestPlanV2Enactment_FailEdgeLoop(t *testing.T) {
	e, _ := ResolveV2Edges(v2StoryBody, "done-review")
	plan, err := PlanV2Enactment(GateDecisionReject, "done-gate", "done-review", "not good enough", e, 0)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.ToStatus != "in_progress" || plan.Exhausted {
		t.Fatalf("reject plan = %+v", plan)
	}
	if len(plan.Rows) != 2 || plan.Rows[0].Kind != "review_reject" || plan.Rows[1].Kind != "status_transition" {
		t.Fatalf("rows = %+v", plan.Rows)
	}
	if plan.Rows[1].Payload["to_status"] != "in_progress" || plan.Rows[1].Payload["on"] != "fail" {
		t.Fatalf("transition payload = %+v", plan.Rows[1].Payload)
	}
}

// TestPlanV2Enactment_Exhaustion pins AC2: the Nth reject lands the story in
// the escalation state via a client-written transition that states the bound.
func TestPlanV2Enactment_Exhaustion(t *testing.T) {
	e, _ := ResolveV2Edges(v2StoryBody, "done-review")
	plan, err := PlanV2Enactment(GateDecisionReject, "done-gate", "done-review", "still failing", e, 2)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !plan.Exhausted || plan.ToStatus != "blocked" {
		t.Fatalf("exhaustion plan = %+v", plan)
	}
	last := plan.Rows[len(plan.Rows)-1]
	if last.Kind != "status_transition" || last.Payload["exhausted"] != true ||
		last.Payload["to_status"] != "blocked" || last.Payload["rejects"] != 3 {
		t.Fatalf("exhaustion row = %+v", last)
	}
	if !strings.Contains(last.Body, "exhausted") {
		t.Fatalf("exhaustion body must state it: %q", last.Body)
	}
}

func TestPlanV2Enactment_Accept(t *testing.T) {
	e, _ := ResolveV2Edges(v2StoryBody, "done-review")
	plan, err := PlanV2Enactment(GateDecisionAccept, "done-gate", "done-review", "ship it", e, 1)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.ToStatus != "done" || plan.Exhausted || len(plan.Rows) != 2 ||
		plan.Rows[0].Kind != "review_accept" || plan.Rows[1].Kind != "status_transition" {
		t.Fatalf("accept plan = %+v", plan)
	}
}

func mkEntry(kind string, payload map[string]any) LedgerEntryView {
	b, _ := json.Marshal(payload)
	return LedgerEntryView{Kind: kind, Payload: b}
}

// TestCountEdgeRejects pins the counting window: rejects for the named state
// only, and the count re-arms at an exhaustion enactment so an operator
// re-arm is not locked out by spent-loop rejects.
func TestCountEdgeRejects(t *testing.T) {
	entries := []LedgerEntryView{
		mkEntry("review_reject", map[string]any{"from_status": "done-review"}),
		mkEntry("review_reject", map[string]any{"from_status": "other-state"}),
		mkEntry("review_reject", map[string]any{"from_status": "done-review"}),
	}
	if got := CountEdgeRejects(entries, "done-review"); got != 2 {
		t.Fatalf("count = %d, want 2", got)
	}
	// Exhaustion re-arms the window.
	entries = append(entries,
		mkEntry("status_transition", map[string]any{"to_status": "blocked", "exhausted": true}),
		mkEntry("review_reject", map[string]any{"from_status": "done-review"}),
	)
	if got := CountEdgeRejects(entries, "done-review"); got != 1 {
		t.Fatalf("count after exhaustion = %d, want 1", got)
	}
	// A plain (non-exhaustion) transition does not re-arm.
	entries = append(entries, mkEntry("status_transition", map[string]any{"to_status": "in_progress", "on": "fail"}))
	if got := CountEdgeRejects(entries, "done-review"); got != 1 {
		t.Fatalf("count after plain transition = %d, want 1", got)
	}
}

// TestPlanV2Enactment_Refusals pins the malformed-shape errors.
func TestPlanV2Enactment_Refusals(t *testing.T) {
	if _, err := PlanV2Enactment(GateDecisionAccept, "g", "s", "", V2Edges{}, 0); err == nil {
		t.Fatal("non-v2 edges must refuse")
	}
	if _, err := PlanV2Enactment("maybe", "g", "s", "", V2Edges{IsV2: true, PassTo: "a", FailTo: "b"}, 0); err == nil {
		t.Fatal("unmapped decision must refuse")
	}
	if _, err := PlanV2Enactment(GateDecisionReject, "g", "s", "",
		V2Edges{IsV2: true, PassTo: "a", FailTo: "b", MaxIterations: 1, OnExhausted: ""}, 0); err == nil {
		t.Fatal("bounded fail edge without on_exhausted must refuse")
	}
}

// TestCheckpointEdge pins the checkpoint-trigger resolution (story 6): a
// state's single ungated trigger edge resolves; review states (on-edges) and
// gated/multiple/absent triggers do not.
func TestCheckpointEdge(t *testing.T) {
	if to, ok := CheckpointEdge(v2StoryBody, "in_progress"); !ok || to != "techdebt-review" {
		t.Fatalf("checkpoint edge = %q/%v, want techdebt-review/true", to, ok)
	}
	if _, ok := CheckpointEdge(v2StoryBody, "done-review"); ok {
		t.Fatal("a review state must not resolve a checkpoint edge")
	}
	if _, ok := CheckpointEdge(v2StoryBody, "blocked"); ok {
		t.Fatal("a terminal state must not resolve a checkpoint edge")
	}
	legacy := "# t\n\n```yaml\nstates: [a, done]\ntransitions:\n  - {from: a, to: done, reviewer_skill: x}\n```\n"
	if _, ok := CheckpointEdge(legacy, "a"); ok {
		t.Fatal("a gated legacy edge must not resolve as checkpoint")
	}
}

// satellitesWFBody is this repo's reviewers-only lifecycle shape (the workflow
// the quirk story sty_21d2c535 governs): a reviewer-gated entry edge into the
// executor's first work state, then an ungated checkpoint to shipping.
const satellitesWFBody = "# satellites-workflow\n\n## Workflow\n\n```yaml\n" +
	"states:\n" +
	"  - backlog\n" +
	"  - {name: in_progress, actor: executor}\n" +
	"  - {name: shipping,    actor: executor}\n" +
	"  - {name: blocked,     actor: operator}\n" +
	"  - done\n" +
	"transitions:\n" +
	"  - {from: backlog,     to: in_progress, reviewer_skill: \"satellites-intent-plan-review\"}\n" +
	"  - {from: in_progress, to: shipping,    trigger: checkpoint}\n" +
	"  - {from: shipping,    to: done,        reviewer_skill: \"satellites-commit-push-review\", on: pass}\n" +
	"  - {from: shipping,    to: in_progress, reviewer_skill: \"satellites-commit-push-review\", on: fail, max_iterations: 3, on_exhausted: blocked}\n" +
	"```\n"

// TestCheckpointEdge_EntryDoesNotChain pins AC3 of sty_21d2c535 at the decision
// layer: the reviewer-gated entry edge (backlog → in_progress) is NOT a
// checkpoint, so an entry-gate transition has nothing to chain into and cannot
// reach shipping in one move — it lands at the executor's first work state. The
// checkpoint lives only at in_progress, advancing to shipping deliberately.
func TestCheckpointEdge_EntryDoesNotChain(t *testing.T) {
	if _, ok := CheckpointEdge(satellitesWFBody, "backlog"); ok {
		t.Fatal("the reviewer-gated entry state must not resolve a checkpoint edge — the entry gate must land at in_progress, not chain to shipping")
	}
	if to, ok := CheckpointEdge(satellitesWFBody, "in_progress"); !ok || to != "shipping" {
		t.Fatalf("in_progress checkpoint = %q/%v, want shipping/true", to, ok)
	}
	if _, ok := CheckpointEdge(satellitesWFBody, "shipping"); ok {
		t.Fatal("shipping carries on:pass|fail edges — it must not resolve as an ungated checkpoint")
	}
}
