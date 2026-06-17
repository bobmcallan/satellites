package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/verb"
)

// TestRecordGateVerdictTo_RowShape pins the ledger row a checkpoint gate
// writes for its verdict (epic:graduated-workflow gate-verdict-evidence AC2/
// AC4): kind ci_result, keyed to the engaged story, payload carrying
// {gate, verdict, blocking_findings, duration_ms}.
func TestRecordGateVerdictTo_RowShape(t *testing.T) {
	stateDB := filepath.Join(t.TempDir(), "state.db")
	var captured []json.RawMessage
	appendLedger := func(_ context.Context, req json.RawMessage) error {
		captured = append(captured, req)
		return nil
	}
	var out bytes.Buffer
	recordGateVerdictTo(context.Background(), stateDB, "sty_fixture", "techdebt-review",
		gateVerdictBlocked, 3, 1500*time.Millisecond, &out, appendLedger)

	if len(captured) != 1 {
		t.Fatalf("expected one ledger append, got %d", len(captured))
	}
	var req verb.LedgerAppendRequest
	if err := json.Unmarshal(captured[0], &req); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if req.StoryID != "sty_fixture" || req.Kind != "ci_result" {
		t.Fatalf("row keyed wrong: %+v", req)
	}
	if !strings.Contains(req.Body, "techdebt-review") || !strings.Contains(req.Body, gateVerdictBlocked) {
		t.Fatalf("body must name gate and verdict: %q", req.Body)
	}
	var payload map[string]any
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload["gate"] != "techdebt-review" || payload["verdict"] != gateVerdictBlocked ||
		payload["blocking_findings"] != float64(3) || payload["duration_ms"] != float64(1500) {
		t.Fatalf("payload = %+v", payload)
	}
	if !strings.Contains(out.String(), "gate verdict recorded") {
		t.Fatalf("stdout must confirm the write: %q", out.String())
	}

	// CLEAN is recorded too (AC1: both verdicts).
	recordGateVerdictTo(context.Background(), stateDB, "sty_fixture", "workflow-check",
		gateVerdictClean, 0, time.Second, &out, appendLedger)
	if len(captured) != 2 {
		t.Fatalf("CLEAN verdict must also append, got %d rows", len(captured))
	}
}

// TestRecordGateVerdictTo_NoEngagementNoop pins AC2's no-engagement
// behaviour: empty story → no write, no error, no output.
func TestRecordGateVerdictTo_NoEngagementNoop(t *testing.T) {
	stateDB := filepath.Join(t.TempDir(), "state.db")
	calls := 0
	appendLedger := func(_ context.Context, _ json.RawMessage) error { calls++; return nil }
	var out bytes.Buffer
	recordGateVerdictTo(context.Background(), stateDB, "", "techdebt-review",
		gateVerdictClean, 0, time.Second, &out, appendLedger)
	if calls != 0 || out.Len() != 0 {
		t.Fatalf("no engagement must be a silent no-op: calls=%d out=%q", calls, out.String())
	}
}

// TestRecordGateVerdictTo_AppendFailureWarnsOnly pins that a ledger failure
// never breaks the gate: the recorder warns and returns.
func TestRecordGateVerdictTo_AppendFailureWarnsOnly(t *testing.T) {
	stateDB := filepath.Join(t.TempDir(), "state.db")
	appendLedger := func(_ context.Context, _ json.RawMessage) error {
		return context.DeadlineExceeded
	}
	var out bytes.Buffer
	recordGateVerdictTo(context.Background(), stateDB, "sty_fixture", "surface-check",
		gateVerdictClean, 0, time.Second, &out, appendLedger)
	if !strings.Contains(out.String(), "warn:") || !strings.Contains(out.String(), "gate verdict unaffected") {
		t.Fatalf("append failure must warn without failing: %q", out.String())
	}
}
