package workflow

import "testing"

// fixWorkflow is the canonical fix workflow: backlog → in_progress → done.
func fixWorkflow() *Workflow {
	return &Workflow{
		States: []State{{Name: "backlog"}, {Name: "in_progress"}, {Name: "done"}},
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

// TestParseBody: parse a `## Workflow` yaml block from a story BODY (no skill
// frontmatter) and derive editability — what work init uses to stamp the door's
// editable flag.
func TestParseBody(t *testing.T) {
	body := []byte("# A story\n\nsome prose\n\n```yaml\nstates:\n  - backlog\n  - in_progress\n  - done\ntransitions:\n  - {from: backlog, to: in_progress, reviewer_skill: plan}\n  - {from: in_progress, to: done, reviewer_skill: done}\n```\n")
	wf, err := ParseBody(body)
	if err != nil {
		t.Fatalf("ParseBody: %v", err)
	}
	if !wf.IsEditable("in_progress") || wf.IsEditable("backlog") || wf.IsEditable("done") {
		t.Errorf("editable derivation wrong: editable=%v", wf.EditableStates())
	}
	if !wf.HasState("done") || wf.HasState("nope") {
		t.Errorf("HasState wrong")
	}
	if _, err := ParseBody([]byte("# no workflow block here")); err == nil {
		t.Errorf("a body with no yaml block should error")
	}
}

// TestValidateLifecycle: a clear begin→work→end workflow passes (incl. an
// epic-style backlog→done with no work phase); a cyclic (no-terminal) or
// single-state (no transitions) workflow fails loudly (AC1/AC2/AC4).
func TestValidateLifecycle(t *testing.T) {
	if err := fixWorkflow().ValidateLifecycle(); err != nil {
		t.Errorf("fix workflow should pass: %v", err)
	}
	// Epic/parent style: backlog → done, no editable middle — still valid.
	parent := &Workflow{
		States:      []State{{Name: "backlog"}, {Name: "done"}},
		Transitions: []Transition{{From: "backlog", To: "done", ReviewerSkill: "close"}},
	}
	if err := parent.ValidateLifecycle(); err != nil {
		t.Errorf("backlog→done parent workflow should pass (empty editable set is allowed): %v", err)
	}
	// Cyclic: every state has an outgoing edge ⇒ no terminal ⇒ fail.
	cyclic := &Workflow{
		States:      []State{{Name: "a"}, {Name: "b"}},
		Transitions: []Transition{{From: "a", To: "b"}, {From: "b", To: "a"}},
	}
	if err := cyclic.ValidateLifecycle(); err == nil {
		t.Errorf("cyclic workflow (no terminal) should fail")
	}
	// Single state: initial == terminal, no transitions ⇒ no editable derivable.
	single := &Workflow{States: []State{{Name: "only"}}}
	if err := single.ValidateLifecycle(); err == nil {
		t.Errorf("single-state workflow should fail (no derivable editable path)")
	}
}

// TestStateRoles_MultiMiddle: a longer pipeline — every middle state is editable,
// only the final state is terminal (no hard-coded names).
func TestStateRoles_MultiMiddle(t *testing.T) {
	w := &Workflow{
		States: []State{{Name: "backlog"}, {Name: "planned"}, {Name: "in_progress"}, {Name: "test"}, {Name: "deploy"}, {Name: "done"}},
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
