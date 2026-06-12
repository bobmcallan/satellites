package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestFormatLedgerRow pins the row rendering: a status_transition payload is
// annotated from → to; other kinds show their body's first line (sty_eb4876e9
// AC2).
func TestFormatLedgerRow(t *testing.T) {
	ts := time.Date(2026, 6, 12, 5, 0, 1, 0, time.UTC)

	payload, _ := json.Marshal(map[string]string{"from_status": "in_progress", "to_status": "done-review"})
	row := formatLedgerRow(ts, "status_transition", "reviewer", "techdebt pass", payload)
	for _, want := range []string{"2026-06-12 05:00:01", "status_transition", "reviewer", "in_progress → done-review", "techdebt pass"} {
		if !strings.Contains(row, want) {
			t.Errorf("transition row missing %q: %s", want, row)
		}
	}

	row = formatLedgerRow(ts, "gate_verdict", "", "CLEAN — commit may proceed\nsecond line", nil)
	if !strings.Contains(row, "CLEAN — commit may proceed") || strings.Contains(row, "second line") {
		t.Errorf("non-transition row must show only the body's first line: %s", row)
	}
	if !strings.Contains(row, " - ") && !strings.Contains(row, "-") {
		t.Errorf("empty actor must render as a dash: %s", row)
	}

	// A transition row with an unparsable payload falls back to the body line.
	row = formatLedgerRow(ts, "status_transition", "x", "fallback body", json.RawMessage("not-json"))
	if !strings.Contains(row, "fallback body") || strings.Contains(row, "→") {
		t.Errorf("unparsable payload must fall back to body: %s", row)
	}
}
