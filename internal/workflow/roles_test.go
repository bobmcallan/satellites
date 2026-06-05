package workflow

import "testing"

// fixWorkflow is the canonical fix workflow: backlog → in_progress → done.
func fixWorkflow() *Workflow {
	return &Workflow{
		States: []string{"backlog", "in_progress", "done"},
		Transitions: []Transition{
			{From: "backlog", To: "in_progress", ReviewerSkill: "plan"},
			{From: "in_progress", To: "done", ReviewerSkill: "done"},
		},
	}
}

// TestStateRoles_FixWorkflow: initial=backlog, terminal=done, editable=in_progress —
// derived from the shape, so the door admits edits only in in_progress (AC4).
func TestStateRoles_FixWorkflow(t *testing.T) {
	w := fixWorkflow()
	if got := w.InitialState(); got != "backlog" {
		t.Errorf("InitialState = %q, want backlog", got)
	}
	if !w.IsTerminal("done") {
		t.Errorf("done should be terminal")
	}
	if w.IsTerminal("in_progress") {
		t.Errorf("in_progress must not be terminal")
	}
	if term := w.TerminalStates(); len(term) != 1 || term[0] != "done" {
		t.Errorf("TerminalStates = %v, want [done]", term)
	}
	if !w.IsEditable("in_progress") {
		t.Errorf("in_progress should be editable")
	}
	if w.IsEditable("backlog") {
		t.Errorf("backlog (initial) must NOT be editable")
	}
	if w.IsEditable("done") {
		t.Errorf("done (terminal) must NOT be editable")
	}
	if ed := w.EditableStates(); len(ed) != 1 || ed[0] != "in_progress" {
		t.Errorf("EditableStates = %v, want [in_progress]", ed)
	}
	if w.IsEditable("bogus") {
		t.Errorf("an undeclared state must not be editable")
	}
}

// TestStateRoles_MultiMiddle: a longer pipeline — every middle state is editable,
// only the final state is terminal (no hard-coded names).
func TestStateRoles_MultiMiddle(t *testing.T) {
	w := &Workflow{
		States: []string{"backlog", "planned", "in_progress", "test", "deploy", "done"},
		Transitions: []Transition{
			{From: "backlog", To: "planned", ReviewerSkill: "p"},
			{From: "planned", To: "in_progress", ReviewerSkill: "s"},
			{From: "in_progress", To: "test", ReviewerSkill: "t"},
			{From: "test", To: "deploy", ReviewerSkill: "d"},
			{From: "deploy", To: "done", ReviewerSkill: "x"},
		},
	}
	if w.InitialState() != "backlog" {
		t.Errorf("InitialState = %q, want backlog", w.InitialState())
	}
	if got := w.TerminalStates(); len(got) != 1 || got[0] != "done" {
		t.Errorf("TerminalStates = %v, want [done]", got)
	}
	ed := w.EditableStates()
	want := map[string]bool{"planned": true, "in_progress": true, "test": true, "deploy": true}
	if len(ed) != len(want) {
		t.Fatalf("EditableStates = %v, want the 4 middle states", ed)
	}
	for _, s := range ed {
		if !want[s] {
			t.Errorf("unexpected editable state %q", s)
		}
	}
}
