package server

import (
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/processtrace"
	"github.com/bobmcallan/satellites/internal/workflow"
)

// TestPickWorkflow pins the sty_20d71a66 governing-workflow selection: an
// exact applies_to category match wins over a "*" wildcard; the wildcard
// governs any category no exact entry claims; nothing applicable → nil.
func TestPickWorkflow(t *testing.T) {
	parent := &workflow.Workflow{Name: "parent-workflow", AppliesTo: []string{"parent"}}
	all := &workflow.Workflow{Name: "satellites-workflow", AppliesTo: []string{"*"}}
	cands := []*workflow.Workflow{parent, all}

	if got := pickWorkflow(cands, "portal"); got != all {
		t.Errorf("portal should resolve to the wildcard workflow, got %+v", got)
	}
	if got := pickWorkflow(cands, "parent"); got != parent {
		t.Errorf("parent must win by exact match over the wildcard, got %+v", got)
	}
	if got := pickWorkflow([]*workflow.Workflow{parent}, "cli"); got != nil {
		t.Errorf("no exact match and no wildcard should be nil, got %+v", got)
	}
}

// TestResultViewMappers pins the sty_a4257811 Result-view projections: edge
// events map through traceRows in ledger order (the loop stays visible), and
// closeOutView renders recorded values with "—" for absent slots (token
// actuals await a reviewer-metrics source).
func TestResultViewMappers(t *testing.T) {
	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	tr := processtrace.ProcessTrace{
		Transitions: []processtrace.TransitionTrace{{
			From: "vetting", To: "sealed", ReviewerSkill: "qa-seal-review",
			Status: processtrace.StatusAccepted, RejectCount: 1,
			Events: []processtrace.GateEvent{
				{Kind: "review_requested", At: at},
				{Kind: "review_reject", At: at.Add(time.Minute), Notes: "seal leaks"},
				{Kind: "review_requested", At: at.Add(2 * time.Minute)},
				{Kind: "review_accept", At: at.Add(3 * time.Minute), Notes: "sealed tight"},
			},
		}},
		CloseOut: processtrace.CloseOut{
			Estimate:       &processtrace.Estimate{TimeMinutes: 45, Tokens: 120000, Basis: "two craters"},
			ElapsedMinutes: 60,
			TotalRejects:   1,
			Terminal:       true,
		},
	}
	rows := traceRows(tr)
	if len(rows) != 1 || len(rows[0].Events) != 4 {
		t.Fatalf("rows/events = %d/%d, want 1/4", len(rows), len(rows[0].Events))
	}
	if rows[0].Events[1].Kind != "review_reject" || rows[0].Events[1].Notes != "seal leaks" {
		t.Errorf("reject event lost its place or notes: %+v", rows[0].Events[1])
	}

	co := closeOutView(tr.CloseOut)
	if co.EstimateTime != "45m" || co.EstimateTokens != "120k" || co.ActualElapsed != "60m" {
		t.Errorf("close-out values wrong: %+v", co)
	}
	if co.ActualTokens != "—" {
		t.Errorf("token actuals must render —, got %q", co.ActualTokens)
	}
	if !co.Terminal || co.TotalRejects != 1 {
		t.Errorf("terminal/rejects wrong: %+v", co)
	}

	// Nothing recorded → every slot dashes, rejects zero.
	empty := closeOutView(processtrace.CloseOut{})
	if empty.EstimateTime != "—" || empty.EstimateTokens != "—" || empty.ActualElapsed != "—" || empty.ActualTokens != "—" {
		t.Errorf("empty close-out must dash all slots: %+v", empty)
	}
}
