package processtrace

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/workflow"
)

var base = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

func ent(kind, body string, payload map[string]any, offsetSec int) LedgerEntry {
	pb, _ := json.Marshal(payload)
	return LedgerEntry{
		Kind:      kind,
		Body:      body,
		Payload:   pb,
		Actor:     "usr_x",
		CreatedAt: base.Add(time.Duration(offsetSec) * time.Second),
	}
}

// fixWorkflow mirrors the satellites-fix-workflow shape:
// backlog →(plan-review) in_progress →(done-review) done.
func fixWorkflow() *workflow.Workflow {
	return &workflow.Workflow{
		Name:   "satellites-fix-workflow",
		States: []workflow.State{{Name: "backlog"}, {Name: "in_progress"}, {Name: "done"}},
		Transitions: []workflow.Transition{
			{From: "backlog", To: "in_progress", ReviewerSkill: "satellites-story-plan-review"},
			{From: "in_progress", To: "done", ReviewerSkill: "satellites-story-done-review"},
		},
	}
}

func find(tr ProcessTrace, from, to string) (TransitionTrace, bool) {
	for _, t := range tr.Transitions {
		if t.From == from && t.To == to {
			return t, true
		}
	}
	return TransitionTrace{}, false
}

// TestReconcile_FullRun_RejectThenAccept mirrors a real story driven to done
// where the done gate rejected once before accepting (sty_cba1d47b's shape):
// both transitions end accepted, the done edge records the reject count, the
// accept verdict supersedes the earlier reject body, and the step summary is
// attached.
func TestReconcile_FullRun_RejectThenAccept(t *testing.T) {
	entries := []LedgerEntry{
		ent("review_requested", "gate plan-review", map[string]any{"gate": "satellites-story-plan-review", "from_status": "backlog", "to_status": "in_progress"}, 0),
		ent("review_accept", "plan ready", map[string]any{"gate": "satellites-story-plan-review", "from_status": "backlog", "to_status": "in_progress"}, 1),
		ent("status_transition", "backlog → in_progress", map[string]any{"from_status": "backlog", "to_status": "in_progress"}, 2),
		ent("review_reject", "AC1 unmet", map[string]any{"gate": "satellites-story-done-review", "from_status": "in_progress"}, 3),
		ent("review_accept", "all ACs met", map[string]any{"gate": "satellites-story-done-review", "from_status": "in_progress", "to_status": "done"}, 4),
		ent("status_transition", "in_progress → done", map[string]any{"from_status": "in_progress", "to_status": "done"}, 5),
		ent("step_summary", "the change closed the gap", map[string]any{"from_status": "in_progress", "to_status": "done", "gate_skill": "satellites-story-done-review", "decision": "accept"}, 6),
	}

	tr := Reconcile("sty_1", "fix", "done", fixWorkflow(), entries, nil)

	plan, ok := find(tr, "backlog", "in_progress")
	if !ok || plan.Status != StatusAccepted {
		t.Fatalf("plan transition: %+v", plan)
	}
	if plan.Verdict != "plan ready" {
		t.Fatalf("plan verdict = %q", plan.Verdict)
	}

	done, ok := find(tr, "in_progress", "done")
	if !ok || done.Status != StatusAccepted {
		t.Fatalf("done transition status = %q want accepted (%+v)", done.Status, done)
	}
	if done.RejectCount != 1 {
		t.Fatalf("done reject count = %d want 1", done.RejectCount)
	}
	if done.Verdict != "all ACs met" {
		t.Fatalf("done verdict = %q want the accept body (accept supersedes reject)", done.Verdict)
	}
	if done.StepSummary != "the change closed the gap" {
		t.Fatalf("done step summary = %q", done.StepSummary)
	}
	if done.At == nil {
		t.Fatalf("done transition should carry a fired-at timestamp")
	}
}

// TestReconcile_CloseOutFromTags reads the plan estimate and actual tokens from
// the story's KV tags (set by `story estimate` / `story actual`) into the
// close-out, and lets an explicit actual-minutes tag override derived elapsed.
func TestReconcile_CloseOutFromTags(t *testing.T) {
	entries := []LedgerEntry{
		ent("review_accept", "plan ready", map[string]any{"gate": "satellites-story-plan-review", "from_status": "backlog", "to_status": "in_progress"}, 0),
		ent("status_transition", "backlog → in_progress", map[string]any{"from_status": "backlog", "to_status": "in_progress"}, 60),
		ent("status_transition", "in_progress → done", map[string]any{"from_status": "in_progress", "to_status": "done"}, 600),
	}
	tags := []string{
		"workflow:satellites-fix-workflow",
		"estimate-minutes:30",
		"estimate-tokens:40000",
		"estimate-basis:2 files, gate loop x3",
		"actual-tokens:38000",
		"actual-minutes:28",
	}
	co := Reconcile("sty_est", "fix", "done", fixWorkflow(), entries, tags).CloseOut
	if co.Estimate == nil {
		t.Fatalf("estimate not read from tags")
	}
	if co.Estimate.TimeMinutes != 30 || co.Estimate.Tokens != 40000 || co.Estimate.Basis != "2 files, gate loop x3" {
		t.Fatalf("estimate = %+v", co.Estimate)
	}
	if co.TokensActual == nil || *co.TokensActual != 38000 {
		t.Fatalf("tokens actual = %v want 38000", co.TokensActual)
	}
	if co.ElapsedMinutes != 28 {
		t.Fatalf("elapsed = %d want 28 (actual-minutes override)", co.ElapsedMinutes)
	}
}

// TestReconcile_CloseOutNoTags leaves estimate/actual empty when the story
// records none — derived elapsed stands and tokens actual stays nil.
func TestReconcile_CloseOutNoTags(t *testing.T) {
	entries := []LedgerEntry{
		ent("status_transition", "backlog → in_progress", map[string]any{"from_status": "backlog", "to_status": "in_progress"}, 60),
		ent("status_transition", "in_progress → done", map[string]any{"from_status": "in_progress", "to_status": "done"}, 600),
	}
	co := Reconcile("sty_noest", "fix", "done", fixWorkflow(), entries, nil).CloseOut
	if co.Estimate != nil {
		t.Fatalf("estimate should be nil, got %+v", co.Estimate)
	}
	if co.TokensActual != nil {
		t.Fatalf("tokens actual should be nil, got %d", *co.TokensActual)
	}
	if co.ElapsedMinutes != 9 { // 600s - 60s = 540s ≈ 9m derived
		t.Fatalf("elapsed = %d want 9 (derived)", co.ElapsedMinutes)
	}
}

// TestReconcile_Pending marks the next outgoing edge from the current status
// as pending when it has not fired.
func TestReconcile_Pending(t *testing.T) {
	entries := []LedgerEntry{
		ent("review_accept", "plan ready", map[string]any{"gate": "satellites-story-plan-review", "from_status": "backlog", "to_status": "in_progress"}, 1),
		ent("status_transition", "backlog → in_progress", map[string]any{"from_status": "backlog", "to_status": "in_progress"}, 2),
	}
	tr := Reconcile("sty_2", "fix", "in_progress", fixWorkflow(), entries, nil)

	done, _ := find(tr, "in_progress", "done")
	if done.Status != StatusPending {
		t.Fatalf("done transition status = %q want pending", done.Status)
	}
	plan, _ := find(tr, "backlog", "in_progress")
	if plan.Status != StatusAccepted {
		t.Fatalf("plan transition status = %q want accepted", plan.Status)
	}
}

// TestReconcile_RejectedNotAdvanced: the gate rejected and the status stayed,
// so the transition reads rejected (not accepted, not pending).
func TestReconcile_RejectedNotAdvanced(t *testing.T) {
	entries := []LedgerEntry{
		ent("review_accept", "plan ready", map[string]any{"gate": "satellites-story-plan-review", "from_status": "backlog", "to_status": "in_progress"}, 1),
		ent("status_transition", "backlog → in_progress", map[string]any{"from_status": "backlog", "to_status": "in_progress"}, 2),
		ent("review_reject", "AC3 has no test", map[string]any{"gate": "satellites-story-done-review", "from_status": "in_progress"}, 3),
	}
	tr := Reconcile("sty_3", "fix", "in_progress", fixWorkflow(), entries, nil)
	done, _ := find(tr, "in_progress", "done")
	if done.Status != StatusRejected {
		t.Fatalf("done status = %q want rejected", done.Status)
	}
	if done.Verdict != "AC3 has no test" || done.RejectCount != 1 {
		t.Fatalf("done verdict=%q rejectCount=%d", done.Verdict, done.RejectCount)
	}
}

// TestReconcile_UnguardedTransitionFires: an unguarded edge that fired reads
// "fired", not "accepted" (no gate judged it).
func TestReconcile_UnguardedTransitionFires(t *testing.T) {
	wf := &workflow.Workflow{
		Name:   "wf",
		States: []workflow.State{{Name: "a"}, {Name: "b"}},
		Transitions: []workflow.Transition{
			{From: "a", To: "b", ReviewerSkill: ""},
		},
	}
	entries := []LedgerEntry{
		ent("status_transition", "a → b", map[string]any{"from_status": "a", "to_status": "b"}, 1),
	}
	tr := Reconcile("sty_4", "fix", "b", wf, entries, nil)
	edge, _ := find(tr, "a", "b")
	if edge.Status != StatusFired {
		t.Fatalf("unguarded edge status = %q want fired", edge.Status)
	}
}

// TestReconcile_NilWorkflow returns an empty, safe trace.
func TestReconcile_NilWorkflow(t *testing.T) {
	tr := Reconcile("sty_5", "fix", "backlog", nil, nil, nil)
	if len(tr.Transitions) != 0 || tr.StoryID != "sty_5" {
		t.Fatalf("nil workflow trace = %+v", tr)
	}
}

// TestReconcile_AlienWorkflowEventsAndCloseOut pins the sty_a4257811
// config-over-code constraint: a workflow whose states and gate names appear
// nowhere in the satellites codebase derives its stages, its full edge event
// sequence (request → reject → re-request → accept → fire: a visible loop),
// and its close-out (estimate from any accept payload's `estimate` object,
// elapsed to the config-derived terminal state, reject totals) — proving the
// reconciler hardcodes no stage, gate, status, or type knowledge.
func TestReconcile_AlienWorkflowEventsAndCloseOut(t *testing.T) {
	wf := &workflow.Workflow{
		Name:   "moonbase-workflow",
		States: []workflow.State{{Name: "draft"}, {Name: "vetting"}, {Name: "sealed"}},
		Transitions: []workflow.Transition{
			{From: "draft", To: "vetting", ReviewerSkill: "lunar-intake-review"},
			{From: "vetting", To: "sealed", ReviewerSkill: "qa-seal-review"},
		},
	}
	entries := []LedgerEntry{
		ent("review_requested", "", map[string]any{"from_status": "draft", "gate": "lunar-intake-review"}, 0),
		ent("review_accept", "intake fine", map[string]any{
			"from_status": "draft", "to_status": "vetting", "gate": "lunar-intake-review",
			"estimate": map[string]any{"time_minutes": 45, "tokens": 120000, "basis": "two craters"},
		}, 60),
		ent("status_transition", "", map[string]any{"from_status": "draft", "to_status": "vetting"}, 61),
		ent("review_requested", "", map[string]any{"from_status": "vetting", "gate": "qa-seal-review"}, 120),
		ent("review_reject", "seal leaks", map[string]any{"from_status": "vetting", "gate": "qa-seal-review"}, 180),
		ent("review_requested", "", map[string]any{"from_status": "vetting", "gate": "qa-seal-review"}, 240),
		ent("review_accept", "sealed tight", map[string]any{"from_status": "vetting", "to_status": "sealed", "gate": "qa-seal-review"}, 3600),
		ent("status_transition", "", map[string]any{"from_status": "vetting", "to_status": "sealed"}, 3601),
	}

	tr := Reconcile("sty_alien", "moon", "sealed", wf, entries, nil)

	seal, ok := find(tr, "vetting", "sealed")
	if !ok || seal.Status != StatusAccepted || seal.RejectCount != 1 {
		t.Fatalf("seal edge wrong: %+v", seal)
	}
	kinds := make([]string, 0, len(seal.Events))
	for _, ev := range seal.Events {
		kinds = append(kinds, ev.Kind)
	}
	want := []string{"review_requested", "review_reject", "review_requested", "review_accept", "status_transition"}
	if len(kinds) != len(want) {
		t.Fatalf("event sequence = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("event sequence = %v, want %v", kinds, want)
		}
	}
	if seal.Events[1].Notes != "seal leaks" {
		t.Errorf("reject notes lost: %+v", seal.Events[1])
	}

	co := tr.CloseOut
	if co.Estimate == nil || co.Estimate.TimeMinutes != 45 || co.Estimate.Tokens != 120000 || co.Estimate.Basis != "two craters" {
		t.Fatalf("estimate not derived from accept payload: %+v", co.Estimate)
	}
	if !co.Terminal {
		t.Error("sealed has no outgoing transition — close-out must read terminal")
	}
	if co.TotalRejects != 1 {
		t.Errorf("total rejects = %d, want 1", co.TotalRejects)
	}
	if co.StartedAt == nil || co.EndedAt == nil || co.ElapsedMinutes != 60 {
		t.Errorf("elapsed wrong: started=%v ended=%v mins=%d", co.StartedAt, co.EndedAt, co.ElapsedMinutes)
	}
	if co.TokensActual != nil {
		t.Error("token actuals must stay nil until a metrics source records them")
	}

	// Mid-flight: at vetting the seal edge reads pending, intake accepted.
	mid := Reconcile("sty_alien", "moon", "vetting", wf, entries[:4], nil)
	if got, _ := find(mid, "vetting", "sealed"); got.Status != StatusPending {
		t.Errorf("mid-flight seal edge = %s, want pending", got.Status)
	}
	if mid.CloseOut.Terminal {
		t.Error("vetting has an outgoing transition — not terminal")
	}
}

// TestAnnotateLedger pins the merged-view projection on alien names
// (epic:graduated-workflow ledger-log-merge): spine rows resolve their
// transition via the same keys Reconcile uses, the exhaustion enactment
// resolves through the fail edge's on_exhausted (no declared from→to edge),
// checkpoint gate and CI ci_result rows carry their payloads through, and
// unknown kinds pass through unbadged. nil workflow → no transition badges.
func TestAnnotateLedger(t *testing.T) {
	wf := &workflow.Workflow{
		Name: "moonbase-workflow",
		States: []workflow.State{
			{Name: "forging", Actor: "smith"}, {Name: "vetting", Actor: "oracle"},
			{Name: "marooned", Actor: "harbourmaster"}, {Name: "sealed"},
		},
		Transitions: []workflow.Transition{
			{From: "forging", To: "vetting", Trigger: "checkpoint"},
			{From: "vetting", To: "sealed", On: "pass"},
			{From: "vetting", To: "forging", On: "fail", MaxIterations: 2, OnExhausted: "marooned"},
		},
	}
	at := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	entries := []LedgerEntry{
		{Kind: "review_reject", Body: "leaks", Payload: []byte(`{"from_status":"vetting","gate":"qa-seal"}`), CreatedAt: at},
		{Kind: "status_transition", Payload: []byte(`{"from_status":"vetting","to_status":"forging","on":"fail"}`), CreatedAt: at.Add(time.Minute)},
		{Kind: "status_transition", Payload: []byte(`{"from_status":"vetting","to_status":"marooned","exhausted":true}`), CreatedAt: at.Add(2 * time.Minute)},
		{Kind: "ci_result", Payload: []byte(`{"gate":"crater-scan","verdict":"BLOCKED","blocking_findings":4}`), CreatedAt: at.Add(3 * time.Minute)},
		{Kind: "ci_result", Payload: []byte(`{"stage":"deploy","result":"success"}`), CreatedAt: at.Add(4 * time.Minute)},
		{Kind: "engagement:phase", Body: "noise", CreatedAt: at.Add(5 * time.Minute)},
	}
	got := AnnotateLedger(wf, entries)
	if len(got) != 6 {
		t.Fatalf("annotated = %d, want 6", len(got))
	}
	if got[0].Transition != "vetting → forging" {
		t.Errorf("reject badge = %q (reject keys on from+gate; the fail edge is its loop)", got[0].Transition)
	}
	if got[1].Transition != "vetting → forging" || got[1].Exhausted {
		t.Errorf("fail-edge move badge wrong: %+v", got[1])
	}
	if got[2].Transition != "vetting → marooned" || !got[2].Exhausted {
		t.Errorf("exhaustion must resolve via on_exhausted: %+v", got[2])
	}
	if got[3].Gate != "crater-scan" || got[3].GateVerdict != "BLOCKED" || got[3].BlockingFindings != 4 {
		t.Errorf("gate verdict row wrong: %+v", got[3])
	}
	if got[4].Stage != "deploy" || got[4].GateVerdict != "success" {
		t.Errorf("ci stage row wrong: %+v", got[4])
	}
	if got[5].Transition != "" || got[5].Gate != "" || got[5].Stage != "" {
		t.Errorf("unknown kind must pass through unbadged: %+v", got[5])
	}
	// nil workflow: rows pass through, gate/ci badges still resolve.
	bare := AnnotateLedger(nil, entries)
	if bare[0].Transition != "" || bare[3].Gate != "crater-scan" {
		t.Errorf("nil-workflow annotation wrong: %+v / %+v", bare[0], bare[3])
	}
}
