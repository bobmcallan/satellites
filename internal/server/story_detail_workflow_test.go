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

// TestMergedLedgerMappers pins the merged Ledger/Log projections
// (epic:graduated-workflow ledger-log-merge) on an ALIEN workflow — every
// state, gate, and actor name is invented, so any hardcoded knowledge in the
// renderer fails this test. The loop (reject → fail edge → re-request →
// accept), the exhaustion enactment, and a checkpoint gate verdict row all
// surface as badges; closeOutView renders recorded values with "—" for
// absent slots.
func TestMergedLedgerMappers(t *testing.T) {
	wfBody := []byte("# moon\n\n```yaml\n" +
		"states:\n" +
		"  - {name: forging, actor: smith}\n" +
		"  - {name: vetting, actor: oracle}\n" +
		"  - {name: marooned, actor: harbourmaster}\n" +
		"  - sealed\n" +
		"transitions:\n" +
		"  - {from: forging, to: vetting, trigger: checkpoint}\n" +
		"  - {from: vetting, on: pass, to: sealed}\n" +
		"  - {from: vetting, on: fail, to: forging, max_iterations: 2, on_exhausted: marooned}\n" +
		"```\n")
	wf, err := workflow.ParseBody(wfBody)
	if err != nil {
		t.Fatalf("alien workflow must parse: %v", err)
	}
	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	mk := func(kind, body, payload string, offset int) processtrace.LedgerEntry {
		return processtrace.LedgerEntry{Kind: kind, Body: body, Payload: []byte(payload), CreatedAt: at.Add(time.Duration(offset) * time.Minute)}
	}
	entries := []processtrace.LedgerEntry{
		mk("ci_result", "gate crater-scan: CLEAN", `{"gate":"crater-scan","verdict":"CLEAN","blocking_findings":0,"duration_ms":900}`, 0),
		mk("review_requested", "gate qa-seal: from vetting", `{"from_status":"vetting","gate":"qa-seal"}`, 1),
		mk("review_reject", "seal leaks", `{"from_status":"vetting","gate":"qa-seal","on":"fail"}`, 2),
		mk("status_transition", "vetting → forging (fail edge)", `{"from_status":"vetting","to_status":"forging","on":"fail"}`, 3),
		mk("review_reject", "still leaking", `{"from_status":"vetting","gate":"qa-seal","on":"fail"}`, 4),
		mk("status_transition", "exhausted: 2/2 rejects on vetting — escalating to marooned", `{"from_status":"vetting","to_status":"marooned","exhausted":true}`, 5),
	}
	rows := mergedRows(processtrace.AnnotateLedger(wf, entries))
	if len(rows) != 6 {
		t.Fatalf("rows = %d, want 6", len(rows))
	}
	// Newest-first: rows[0] is the exhaustion enactment.
	if rows[0].BadgeClass != "exhausted" || rows[0].Transition != "vetting → marooned" {
		t.Errorf("exhaustion row badge wrong: %+v", rows[0])
	}
	// The fail-edge move badges with the alien edge it traversed.
	if rows[2].Kind != "status_transition" || rows[2].Transition != "vetting → forging" {
		t.Errorf("fail-edge row badge wrong: %+v", rows[2])
	}
	// Spine rows badge with the alien transition; reject matched by from+gate.
	if rows[3].Kind != "review_reject" || rows[3].Transition == "" {
		t.Errorf("reject row must carry a transition badge: %+v", rows[3])
	}
	// The checkpoint gate verdict renders inline with gate + verdict.
	last := rows[5]
	if last.Kind != "ci_result" || last.BadgeClass != "gate-clean" || last.BadgeLabel != "crater-scan: CLEAN" {
		t.Errorf("gate verdict row wrong: %+v", last)
	}

	co := closeOutView(processtrace.CloseOut{
		Estimate:       &processtrace.Estimate{TimeMinutes: 45, Tokens: 120000, Basis: "two craters"},
		ElapsedMinutes: 60,
		TotalRejects:   2,
		Terminal:       true,
	})
	if co.EstimateTime != "45m" || co.EstimateTokens != "120k" || co.ActualElapsed != "60m" {
		t.Errorf("close-out values wrong: %+v", co)
	}
	if co.ActualTokens != "—" {
		t.Errorf("token actuals must render —, got %q", co.ActualTokens)
	}

	// Nothing recorded → every slot dashes, rejects zero.
	empty := closeOutView(processtrace.CloseOut{})
	if empty.EstimateTime != "—" || empty.EstimateTokens != "—" || empty.ActualElapsed != "—" || empty.ActualTokens != "—" {
		t.Errorf("empty close-out must dash all slots: %+v", empty)
	}
}
