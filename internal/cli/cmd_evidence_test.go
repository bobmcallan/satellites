package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/workstate"
)

var errStubLedger = errors.New("stub: no server")

// TestEvidenceShow_RendersGateAndCI confirms `evidence show` reads a story's
// captured trail from the store and renders gate + CI rows (AC6 readable).
func TestEvidenceShow_RendersGateAndCI(t *testing.T) {
	db := filepath.Join(t.TempDir(), "state.db")
	store, err := workstate.Open(db)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	now := time.Unix(1700000000, 0).UTC()
	mustRec(t, store, workstate.Evidence{Story: "sty_a", Kind: workstate.EvidenceGate, Label: "satellites-story-done-review", Decision: "accept", FromStatus: "in_progress", ToStatus: "done", TS: now})
	mustRec(t, store, workstate.Evidence{Story: "sty_a", Kind: workstate.EvidenceCI, Label: "test", Decision: "success", Ref: "fec9910", TS: now.Add(time.Minute)})
	store.Close()

	var buf bytes.Buffer
	if err := runEvidenceShow(&buf, db, "sty_a", false); err != nil {
		t.Fatalf("show: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"gate", "satellites-story-done-review", "accept", "in_progress→done", "ci", "test", "success", "ref=fec9910"} {
		if !strings.Contains(out, want) {
			t.Errorf("show output missing %q:\n%s", want, out)
		}
	}

	// Empty story → explicit "no captured evidence", not an error.
	var buf2 bytes.Buffer
	if err := runEvidenceShow(&buf2, db, "sty_none", false); err != nil {
		t.Fatalf("show empty: %v", err)
	}
	if !strings.Contains(buf2.String(), "no captured evidence") {
		t.Errorf("empty show wrong: %q", buf2.String())
	}
}

// TestEvidenceShow_JSON confirms --json round-trips the rows.
func TestEvidenceShow_JSON(t *testing.T) {
	db := filepath.Join(t.TempDir(), "state.db")
	store, _ := workstate.Open(db)
	mustRec(t, store, workstate.Evidence{Story: "sty_j", Kind: workstate.EvidenceCI, Label: "deploy", Decision: "failure", TS: time.Unix(1700000000, 0).UTC()})
	store.Close()

	var buf bytes.Buffer
	if err := runEvidenceShow(&buf, db, "sty_j", true); err != nil {
		t.Fatalf("show json: %v", err)
	}
	var rows []evidenceRow
	if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
		t.Fatalf("decode json: %v\n%s", err, buf.String())
	}
	if len(rows) != 1 || rows[0].Kind != "ci" || rows[0].Decision != "failure" {
		t.Fatalf("json rows wrong: %+v", rows)
	}
}

// TestEvidenceCI_CapturesLocallyEvenWhenLedgerUnreachable pins AC2: the CI
// outcome lands in the store even though the ledger dispatch (no server in a
// unit test) fails — the local capture is not lost.
func TestEvidenceCI_CapturesLocallyEvenWhenLedgerUnreachable(t *testing.T) {
	db := filepath.Join(t.TempDir(), "state.db")
	var buf bytes.Buffer
	// Injected appender fails (simulating an unreachable server); the command
	// must still record locally and only warn on the ledger miss.
	failAppend := func(context.Context, json.RawMessage) error { return errStubLedger }
	err := runEvidenceCI(context.Background(), &buf, evidenceCIOpts{
		StateDB: db, Story: "sty_ci", Stage: "test", Result: "success", Ref: "deadbeef",
	}, failAppend)
	if err != nil {
		t.Fatalf("ci record should not hard-fail on ledger miss: %v", err)
	}
	if !strings.Contains(buf.String(), "warn: ledger_append ci_result failed") {
		t.Errorf("expected a ledger-miss warning, got: %q", buf.String())
	}
	store, _ := workstate.Open(db)
	defer store.Close()
	rows, _ := store.ListEvidence("sty_ci")
	if len(rows) != 1 || rows[0].Kind != "ci" || rows[0].Label != "test" || rows[0].Decision != "success" || rows[0].Ref != "deadbeef" {
		t.Fatalf("ci not captured locally: %+v", rows)
	}
}

func mustRec(t *testing.T, s *workstate.Store, e workstate.Evidence) {
	t.Helper()
	if _, err := s.RecordEvidence(e); err != nil {
		t.Fatalf("record evidence: %v", err)
	}
}
