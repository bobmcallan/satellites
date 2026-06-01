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
		States: []string{"backlog", "in_progress", "done"},
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

	tr := Reconcile("sty_1", "fix", "done", fixWorkflow(), entries)

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

// TestReconcile_Pending marks the next outgoing edge from the current status
// as pending when it has not fired.
func TestReconcile_Pending(t *testing.T) {
	entries := []LedgerEntry{
		ent("review_accept", "plan ready", map[string]any{"gate": "satellites-story-plan-review", "from_status": "backlog", "to_status": "in_progress"}, 1),
		ent("status_transition", "backlog → in_progress", map[string]any{"from_status": "backlog", "to_status": "in_progress"}, 2),
	}
	tr := Reconcile("sty_2", "fix", "in_progress", fixWorkflow(), entries)

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
	tr := Reconcile("sty_3", "fix", "in_progress", fixWorkflow(), entries)
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
		States: []string{"a", "b"},
		Transitions: []workflow.Transition{
			{From: "a", To: "b", ReviewerSkill: ""},
		},
	}
	entries := []LedgerEntry{
		ent("status_transition", "a → b", map[string]any{"from_status": "a", "to_status": "b"}, 1),
	}
	tr := Reconcile("sty_4", "fix", "b", wf, entries)
	edge, _ := find(tr, "a", "b")
	if edge.Status != StatusFired {
		t.Fatalf("unguarded edge status = %q want fired", edge.Status)
	}
}

// TestReconcile_NilWorkflow returns an empty, safe trace.
func TestReconcile_NilWorkflow(t *testing.T) {
	tr := Reconcile("sty_5", "fix", "backlog", nil, nil)
	if len(tr.Transitions) != 0 || tr.StoryID != "sty_5" {
		t.Fatalf("nil workflow trace = %+v", tr)
	}
}
