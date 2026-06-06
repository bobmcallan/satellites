package workstate

import (
	"testing"
	"time"
)

// TestRecordAndListEvidence pins AC1/AC6: a gate run + a CI outcome are recorded
// durably, story-linked, and read back oldest-first; reads are per-story.
func TestRecordAndListEvidence(t *testing.T) {
	s := openTemp(t)
	now := time.Unix(1700000000, 0).UTC()

	if _, err := s.RecordEvidence(Evidence{
		Story: "sty_a", Kind: EvidenceGate, Label: "satellites-story-plan-review",
		Decision: "accept", FromStatus: "backlog", ToStatus: "in_progress", TS: now,
	}); err != nil {
		t.Fatalf("record gate: %v", err)
	}
	if _, err := s.RecordEvidence(Evidence{
		Story: "sty_a", Kind: EvidenceCI, Label: "test", Decision: "success",
		Ref: "abc123", TS: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("record ci: %v", err)
	}
	// A different story's evidence must not bleed in.
	if _, err := s.RecordEvidence(Evidence{Story: "sty_b", Kind: EvidenceGate, Label: "g", Decision: "reject", TS: now}); err != nil {
		t.Fatalf("record other: %v", err)
	}

	rows, err := s.ListEvidence("sty_a")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("sty_a evidence = %d rows, want 2", len(rows))
	}
	if rows[0].Kind != EvidenceGate || rows[0].Decision != "accept" || rows[0].ToStatus != "in_progress" {
		t.Fatalf("gate row wrong: %+v", rows[0])
	}
	if rows[1].Kind != EvidenceCI || rows[1].Label != "test" || rows[1].Ref != "abc123" {
		t.Fatalf("ci row wrong: %+v", rows[1])
	}
}

// TestRecordEvidenceRejectsIncomplete: a row needs a story and a kind.
func TestRecordEvidenceRejectsIncomplete(t *testing.T) {
	s := openTemp(t)
	if _, err := s.RecordEvidence(Evidence{Kind: EvidenceGate}); err == nil {
		t.Fatalf("expected error for missing story")
	}
	if _, err := s.RecordEvidence(Evidence{Story: "sty_a"}); err == nil {
		t.Fatalf("expected error for missing kind")
	}
}

// TestEvidenceSurvivesCleanupWork pins AC4: evidence is the DURABLE trail —
// terminal CleanupWork drops the transient inbox/claim but never the evidence,
// so a completed story's QA trail survives for the audit + QA view.
func TestEvidenceSurvivesCleanupWork(t *testing.T) {
	s := openTemp(t)
	now := time.Unix(1700000000, 0).UTC()
	if _, err := s.InboxAppend("sty_done", "review_requested", "x", nil, now); err != nil {
		t.Fatalf("inbox: %v", err)
	}
	if _, err := s.RecordEvidence(Evidence{Story: "sty_done", Kind: EvidenceGate, Label: "g", Decision: "accept", TS: now}); err != nil {
		t.Fatalf("evidence: %v", err)
	}
	if err := s.CleanupWork("sty_done"); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	// Transient working state gone...
	if msgs, _ := s.InboxReadAll("sty_done"); len(msgs) != 0 {
		t.Fatalf("inbox should be cleaned, got %d", len(msgs))
	}
	// ...but the durable evidence remains.
	rows, err := s.ListEvidence("sty_done")
	if err != nil {
		t.Fatalf("list after cleanup: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("evidence should survive cleanup, got %d rows", len(rows))
	}
}
