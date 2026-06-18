package cli

import (
	"encoding/json"
	"testing"
)

func TestPlanRepair_Classification(t *testing.T) {
	cases := []struct {
		name     string
		code     string
		status   string
		wantKind repairKind
	}{
		{"unparseable on started story -> auto", "workflow-unparseable", "in_progress", repairAuto},
		{"lifecycle on started story -> auto", "workflow-lifecycle", "blocked", repairAuto},
		{"unparseable on backlog -> expected", "workflow-unparseable", "backlog", repairExpected},
		{"unparseable on ready -> expected", "workflow-unparseable", "ready", repairExpected},
		{"unparseable on plan-reviewed -> expected", "workflow-unparseable", "plan-reviewed", repairExpected},
		{"missing gate skill -> manual", "missing-gate-skill", "in_progress", repairManual},
		{"semantic code -> manual", "principle-conflict", "backlog", repairManual},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := planRepair(validateFinding{StoryID: "sty_x", Code: c.code}, c.status)
			if got.Kind != c.wantKind {
				t.Fatalf("planRepair(%s,%s).Kind = %s, want %s", c.code, c.status, got.Kind, c.wantKind)
			}
			if got.Kind == repairAuto && got.Command == "" {
				t.Errorf("auto action must carry a command")
			}
			if got.Rationale == "" {
				t.Errorf("every action must carry a rationale")
			}
		})
	}
}

func TestPlanRepair_StartedStatusBoundary(t *testing.T) {
	// done/cancelled are terminal and never enumerated, but the helper must not
	// treat them as "started" for repair purposes either.
	for _, s := range []string{"done", "cancelled", "", "unknown"} {
		if startedStatus(s) {
			t.Errorf("startedStatus(%q) should be false", s)
		}
	}
	for _, s := range []string{"in_progress", "techdebt-review", "integration-review", "done-review", "blocked"} {
		if !startedStatus(s) {
			t.Errorf("startedStatus(%q) should be true", s)
		}
	}
}

func TestSummarizeRepair_DeltaAccounting(t *testing.T) {
	actions := []repairAction{
		{Kind: repairAuto, Applied: true},
		{Kind: repairAuto, Applied: false},
		{Kind: repairExpected},
		{Kind: repairManual},
		{Kind: repairManual},
	}
	s := summarizeRepair(actions)
	if s.Auto != 2 || s.Expected != 1 || s.Manual != 2 {
		t.Fatalf("counts wrong: %+v", s)
	}
	if s.Applied != 1 {
		t.Fatalf("applied = %d, want 1", s.Applied)
	}
}

func TestRepairReportJSONShape(t *testing.T) {
	after := 3
	r := repairReport{
		FindingsBefore: 5,
		Actions: []repairAction{
			{StoryID: "sty_x", Code: "workflow-unparseable", Kind: repairAuto, Command: "workflow embed sty_x", Rationale: "r", Applied: true},
		},
		FindingsAfter: &after,
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var back repairReport
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.FindingsBefore != 5 || back.FindingsAfter == nil || *back.FindingsAfter != 3 {
		t.Fatalf("before/after lost: %+v", back)
	}
	if len(back.Actions) != 1 || back.Actions[0].Kind != repairAuto || !back.Actions[0].Applied {
		t.Fatalf("action lost: %+v", back.Actions)
	}
}
