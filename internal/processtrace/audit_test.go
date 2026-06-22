package processtrace

import (
	"encoding/json"
	"testing"
	"time"
)

// entry builds a LedgerEntry at a monotonically-increasing time (i seconds past
// a fixed base) so fixtures express ordering by index.
func entry(i int, kind, body string, payload map[string]any) LedgerEntry {
	base := time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC)
	var pb json.RawMessage
	if payload != nil {
		pb, _ = json.Marshal(payload)
	}
	return LedgerEntry{Kind: kind, Body: body, Payload: pb, CreatedAt: base.Add(time.Duration(i) * time.Second)}
}

// has reports whether findings contain the given anomaly.
func has(fs []Finding, anomaly string) bool {
	for _, f := range fs {
		if f.Anomaly == anomaly {
			return true
		}
	}
	return false
}

// TestAudit_StatusDrift: status ≠ last transition's to_status triggers; equal is clean.
func TestAudit_StatusDrift(t *testing.T) {
	drift := Audit(StoryAudit{StoryID: "sty_a", CurrentStatus: "in_progress", Entries: []LedgerEntry{
		entry(1, kindStatusTransition, "", map[string]any{"from_status": "backlog", "to_status": "done"}),
	}})
	if !has(drift, AnomalyStatusDrift) {
		t.Fatalf("expected status-drift, got %+v", drift)
	}
	clean := Audit(StoryAudit{StoryID: "sty_a", CurrentStatus: "done", Entries: []LedgerEntry{
		entry(1, kindReviewAccept, "ok", map[string]any{"from_status": "in_progress", "gate": "g"}),
		entry(2, kindStatusTransition, "", map[string]any{"from_status": "in_progress", "to_status": "done"}),
	}})
	if has(clean, AnomalyStatusDrift) {
		t.Fatalf("did not expect status-drift, got %+v", clean)
	}
}

// TestAudit_ShipThenNoGate: a transition to done with no preceding accept triggers;
// an accept before it is clean.
func TestAudit_ShipThenNoGate(t *testing.T) {
	bad := Audit(StoryAudit{StoryID: "sty_b", CurrentStatus: "done", Entries: []LedgerEntry{
		entry(1, kindStatusTransition, "", map[string]any{"from_status": "in_progress", "to_status": "done"}),
	}})
	if !has(bad, AnomalyShipThenNoGate) {
		t.Fatalf("expected ship-then-no-gate, got %+v", bad)
	}
	good := Audit(StoryAudit{StoryID: "sty_b", CurrentStatus: "done", Entries: []LedgerEntry{
		entry(1, kindReviewAccept, "ok", map[string]any{"from_status": "in_progress", "gate": "satellites-story-done-review"}),
		entry(2, kindStatusTransition, "", map[string]any{"from_status": "in_progress", "to_status": "done"}),
	}})
	if has(good, AnomalyShipThenNoGate) {
		t.Fatalf("did not expect ship-then-no-gate, got %+v", good)
	}
}

// TestAudit_SpuriousReject: a 401/concurrent reject triggers; a genuine content
// reject does not.
func TestAudit_SpuriousReject(t *testing.T) {
	spur := Audit(StoryAudit{StoryID: "sty_c", CurrentStatus: "in_progress", Entries: []LedgerEntry{
		entry(1, kindReviewRequested, "", map[string]any{"from_status": "in_progress"}),
		entry(2, kindReviewReject, "enact failed: reviewer key returned 401 unauthorized", map[string]any{"from_status": "in_progress", "gate": "g"}),
		entry(3, kindReviewRequested, "", map[string]any{"from_status": "in_progress"}),
	}})
	if !has(spur, AnomalySpuriousReject) {
		t.Fatalf("expected spurious-enact-failure-reject, got %+v", spur)
	}
	genuine := Audit(StoryAudit{StoryID: "sty_c", CurrentStatus: "in_progress", Entries: []LedgerEntry{
		entry(1, kindReviewRequested, "", map[string]any{"from_status": "in_progress"}),
		entry(2, kindReviewReject, "AC3 not met: the test does not cover the empty case", map[string]any{"from_status": "in_progress", "gate": "g"}),
		entry(3, kindReviewRequested, "", map[string]any{"from_status": "in_progress"}),
	}})
	if has(genuine, AnomalySpuriousReject) {
		t.Fatalf("did not expect spurious for a content reject, got %+v", genuine)
	}
}

// TestAudit_RejectNoFollowUp: a trailing reject (no later request) triggers; a
// reject followed by a request is clean.
func TestAudit_RejectNoFollowUp(t *testing.T) {
	stalled := Audit(StoryAudit{StoryID: "sty_d", CurrentStatus: "in_progress", Entries: []LedgerEntry{
		entry(1, kindReviewRequested, "", map[string]any{"from_status": "in_progress"}),
		entry(2, kindReviewReject, "AC2 missing", map[string]any{"from_status": "in_progress", "gate": "g"}),
	}})
	if !has(stalled, AnomalyRejectNoFollowUp) {
		t.Fatalf("expected reject-with-no-follow-up, got %+v", stalled)
	}
	followed := Audit(StoryAudit{StoryID: "sty_d", CurrentStatus: "in_progress", Entries: []LedgerEntry{
		entry(1, kindReviewReject, "AC2 missing", map[string]any{"from_status": "in_progress", "gate": "g"}),
		entry(2, kindReviewRequested, "", map[string]any{"from_status": "in_progress"}),
	}})
	if has(followed, AnomalyRejectNoFollowUp) {
		t.Fatalf("did not expect reject-with-no-follow-up after a follow-up, got %+v", followed)
	}
}

// TestAudit_UngatedDeploy: a deploy=success ci_result with status≠done triggers;
// status==done is clean.
func TestAudit_UngatedDeploy(t *testing.T) {
	ungated := Audit(StoryAudit{StoryID: "sty_e", CurrentStatus: "in_progress", Entries: []LedgerEntry{
		entry(1, kindCIResult, "ci deploy: success", map[string]any{"stage": "deploy", "result": "success", "ref": "v1"}),
	}})
	if !has(ungated, AnomalyUngatedDeploy) {
		t.Fatalf("expected ungated-deploy, got %+v", ungated)
	}
	closed := Audit(StoryAudit{StoryID: "sty_e", CurrentStatus: "done", Entries: []LedgerEntry{
		entry(1, kindReviewAccept, "ok", map[string]any{"from_status": "in_progress"}),
		entry(2, kindStatusTransition, "", map[string]any{"from_status": "in_progress", "to_status": "done"}),
		entry(3, kindCIResult, "ci deploy: success", map[string]any{"stage": "deploy", "result": "success", "ref": "v1"}),
	}})
	if has(closed, AnomalyUngatedDeploy) {
		t.Fatalf("did not expect ungated-deploy for a done story, got %+v", closed)
	}
}

// TestAudit_UnengagedWork: in_progress with no review_requested triggers; with a
// request it is clean; backlog never triggers.
func TestAudit_UnengagedWork(t *testing.T) {
	unengaged := Audit(StoryAudit{StoryID: "sty_f", CurrentStatus: "in_progress", Entries: []LedgerEntry{
		entry(1, kindStepSummary, "did stuff", map[string]any{"from_status": "in_progress"}),
	}})
	if !has(unengaged, AnomalyUnengagedWork) {
		t.Fatalf("expected unengaged-work, got %+v", unengaged)
	}
	engaged := Audit(StoryAudit{StoryID: "sty_f", CurrentStatus: "in_progress", Entries: []LedgerEntry{
		entry(1, kindReviewRequested, "", map[string]any{"from_status": "backlog"}),
	}})
	if has(engaged, AnomalyUnengagedWork) {
		t.Fatalf("did not expect unengaged-work with a request, got %+v", engaged)
	}
	backlog := Audit(StoryAudit{StoryID: "sty_f", CurrentStatus: "backlog", Entries: nil})
	if has(backlog, AnomalyUnengagedWork) {
		t.Fatalf("backlog should not be unengaged-work, got %+v", backlog)
	}
	// A legacy/metadata-only ledger (no process-spine activity) must NOT flag —
	// it never entered the loop (order:9 dogfood: 59 such false positives).
	legacy := Audit(StoryAudit{StoryID: "sty_legacy", CurrentStatus: "done", Entries: []LedgerEntry{
		entry(1, "story_created", "", nil),
		entry(2, "story_updated", "", nil),
	}})
	if has(legacy, AnomalyUnengagedWork) {
		t.Fatalf("metadata-only legacy story should not be unengaged-work, got %+v", legacy)
	}
}

// TestAudit_CleanStoryNoFindings: a well-formed lifecycle produces nothing, and
// findings are addressable by story id.
func TestAudit_CleanStoryNoFindings(t *testing.T) {
	clean := Audit(StoryAudit{StoryID: "sty_clean", CurrentStatus: "done", Entries: []LedgerEntry{
		entry(1, kindReviewRequested, "", map[string]any{"from_status": "backlog"}),
		entry(2, kindReviewAccept, "ok", map[string]any{"from_status": "backlog", "gate": "satellites-story-plan-review"}),
		entry(3, kindStatusTransition, "", map[string]any{"from_status": "backlog", "to_status": "in_progress"}),
		entry(4, kindReviewRequested, "", map[string]any{"from_status": "in_progress"}),
		entry(5, kindReviewAccept, "ok", map[string]any{"from_status": "in_progress", "gate": "satellites-story-done-review"}),
		entry(6, kindStatusTransition, "", map[string]any{"from_status": "in_progress", "to_status": "done"}),
		entry(7, kindCIResult, "ci deploy: success", map[string]any{"stage": "deploy", "result": "success", "ref": "v1"}),
	}})
	if len(clean) != 0 {
		t.Fatalf("clean lifecycle should yield no findings, got %+v", clean)
	}
	// AuditAll concatenates and every finding carries its story id.
	fs := AuditAll([]StoryAudit{
		{StoryID: "sty_x", CurrentStatus: "in_progress", Entries: []LedgerEntry{entry(1, kindStatusTransition, "", map[string]any{"from_status": "backlog", "to_status": "done"})}},
	})
	if len(fs) == 0 {
		t.Fatalf("expected findings from the dirty story")
	}
	for _, f := range fs {
		if f.StoryID != "sty_x" {
			t.Fatalf("finding not addressable by story: %+v", f)
		}
	}
}

// TestAudit_WorkflowDerivedClassifier (sty_9f97ff5c): the detectors classify
// terminal/editable via the caller's GOVERNING-workflow classifier, not a fixed
// status set. A renamed terminal status ("shipped") is recognised as terminal and
// a renamed working status ("doing") as editable — proving the literals are gone.
func TestAudit_WorkflowDerivedClassifier(t *testing.T) {
	has := func(fs []Finding, a string) bool {
		for _, f := range fs {
			if f.Anomaly == a {
				return true
			}
		}
		return false
	}
	term := func(st string) bool { return st == "shipped" }
	edit := func(st string) bool { return st == "doing" }

	// reject-with-no-follow-up: a story at the renamed terminal "shipped" must NOT
	// be flagged stalled — terminal() drives the early return, not a "done" literal.
	notStalled := Audit(StoryAudit{StoryID: "sty_w", CurrentStatus: "shipped", Terminal: term, Editable: edit, Entries: []LedgerEntry{
		entry(1, kindReviewReject, "AC2 missing", map[string]any{"from_status": "doing", "gate": "g"}),
	}})
	if has(notStalled, AnomalyRejectNoFollowUp) {
		t.Errorf("a story at the renamed terminal 'shipped' must not be flagged stalled: %+v", notStalled)
	}

	// ungated-deploy: a deploy=success at the non-terminal renamed status "doing"
	// IS flagged (terminal() returns false for it).
	ungated := Audit(StoryAudit{StoryID: "sty_w2", CurrentStatus: "doing", Terminal: term, Editable: edit, Entries: []LedgerEntry{
		entry(1, kindCIResult, "deploy", map[string]any{"stage": "deploy", "result": "success", "ref": "v1"}),
	}})
	if !has(ungated, AnomalyUngatedDeploy) {
		t.Errorf("deploy at non-terminal 'doing' must flag ungated-deploy: %+v", ungated)
	}
}
