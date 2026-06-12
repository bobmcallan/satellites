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
