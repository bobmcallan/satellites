package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParse_LiveWorkflowSkills pins sty_cce5abc0 AC1/AC6: the project's
// live workflow skills are now `type:skill` `<name>/SKILL.md` artifacts
// carrying skill frontmatter (name/description/version) alongside the
// workflow-only `applies_to` — and `Parse` still extracts their
// states/transitions unchanged despite the added frontmatter keys. A
// regression here means a workflow-as-skill conversion broke the gate's
// ability to read its own workflow.
func TestParse_LiveWorkflowSkills(t *testing.T) {
	cases := []struct {
		path        string
		name        string
		transitions int
	}{
		// The reviewers-only repo workflow: backlog → in_progress → shipping →
		// done, gated by exactly two reviewers that exist (satellites-intent-plan-review
		// opens; satellites-commit-push-review judges the push and enacts the v2
		// shipping pass/fail edges). 4 transitions: 1 gated entry + 1 checkpoint +
		// the shipping on:pass and on:fail edges. The parent (epic/anchor) workflow
		// is one close-reviewer edge. Workflows are repo-owned client-dir config
		// under .satellites/workflows/ (epic:client-dir-separation order-2) — Parse
		// reads the same shape.
		{filepath.Join("..", "..", ".satellites", "workflows", "satellites-workflow.md"), "satellites-workflow", 8},
		// The parent (epic/anchor) workflow is now a GOVERNANCE SOURCE in the
		// config/workflows embed, no longer scaffolded into .satellites/workflows
		// (sty_a69e8c61) — parse it from the embed source.
		{filepath.Join("..", "..", "config", "workflows", "satellites-parent-workflow.md"), "satellites-parent-workflow", 4},
	}
	for _, c := range cases {
		raw, err := os.ReadFile(c.path)
		if err != nil {
			t.Fatalf("read %s: %v", c.path, err)
		}
		wf, err := Parse(raw)
		if err != nil {
			t.Fatalf("parse %s: %v", c.path, err)
		}
		if wf.Name != c.name {
			t.Errorf("%s: name = %q, want %q", c.path, wf.Name, c.name)
		}
		if len(wf.AppliesTo) == 0 {
			t.Errorf("%s: applies_to empty", c.path)
		}
		if len(wf.Transitions) != c.transitions {
			t.Errorf("%s: %d transitions, want %d", c.path, len(wf.Transitions), c.transitions)
		}
		// Every transition must be drivable: gated by a reviewer skill OR
		// deterministically client-enacted (a checkpoint trigger or an
		// on:pass|fail edge) — the loop must never see an edge nothing can
		// drive (sty_3934ad71; v2 semantics epic:graduated-workflow).
		for _, tr := range wf.Transitions {
			if strings.TrimSpace(tr.ReviewerSkill) == "" && tr.On == "" && tr.Trigger == "" {
				// An ungated edge is driven by the executor via `set-status`:
				// the shipping → done-review ship-advance after commit-push is
				// the one such edge. Any OTHER ungated edge is an accidentally
				// undrivable gate.
				if tr.From != "shipping" || tr.To != "done-review" {
					t.Errorf("%s: undrivable transition %s→%s (needs a gate, an on-edge, a trigger, or be the shipping→done-review set-status edge)", c.path, tr.From, tr.To)
				}
			}
		}
	}
}

// TestIsTerminal_ReopenExcluded (sty_781c96aa): a state whose only outgoing edge
// is a trigger:reopen operator escape-hatch is still TERMINAL (and non-editable),
// the entry state is detected despite the reopen's incoming edge, and the reopen
// edge stays visible to TransitionsFrom for the ungated set-status path.
func TestIsTerminal_ReopenExcluded(t *testing.T) {
	input := `---
name: anc
applies_to: [parent]
---

` + "```yaml" + `
states: [backlog, done, cancelled]
transitions:
  - {from: backlog, to: done, reviewer_skill: "close"}
  - {from: backlog, to: cancelled, reviewer_skill: "cancel"}
  - {from: done, to: backlog, trigger: reopen}
  - {from: cancelled, to: backlog, trigger: reopen}
` + "```" + `
`
	wf, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !wf.IsTerminal("done") || !wf.IsTerminal("cancelled") {
		t.Error("a state with only a trigger:reopen outgoing edge must be terminal")
	}
	if wf.IsEditable("done") {
		t.Error("a terminal (reopen-only) state must not be editable")
	}
	if got := wf.TerminalStates(); len(got) != 2 {
		t.Errorf("TerminalStates = %v, want [done cancelled]", got)
	}
	if wf.InitialState() != "backlog" {
		t.Errorf("InitialState = %q, want backlog (reopen edges must not disqualify it)", wf.InitialState())
	}
	if len(wf.TransitionsFrom("done")) != 1 {
		t.Error("TransitionsFrom(done) must still expose the reopen edge for set-status")
	}
}

// TestParse_ShippedExampleFile verifies the example checked into the
// repo at examples/skills/feature-workflow.md round-trips through the
// parser — covers AC#1 against the actual artifact callers see.
func TestParse_ShippedExampleFile(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "skills", "feature-workflow.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	wf, err := Parse(raw)
	if err != nil {
		t.Fatalf("parse example: %v", err)
	}
	if wf.Name != "feature-workflow" {
		t.Fatalf("name = %q, want feature-workflow", wf.Name)
	}
	if _, ok := wf.FindTransition("in_progress", "done"); !ok {
		t.Fatalf("expected in_progress→done in example file")
	}
}

const exampleSkill = `---
name: feature-workflow
applies_to: [feature, fix]
---

# Feature workflow

Backlog → in-progress → completed, with reviewer-gated planning + done
checks. Reviewers named here are skill ids the request_review verb
dispatches.

` + "```yaml" + `
states:
  - backlog
  - planning
  - planned
  - in-progress
  - completed
transitions:
  - {from: backlog,     to: planning,    reviewer_skill: ""}
  - {from: planning,    to: planned,     reviewer_skill: "plan-review"}
  - {from: planned,     to: in-progress, reviewer_skill: ""}
  - {from: in-progress, to: completed,   reviewer_skill: "done-review"}
` + "```" + `
`

func TestParse_RoundTripExample(t *testing.T) {
	wf, err := Parse([]byte(exampleSkill))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if wf.Name != "feature-workflow" {
		t.Fatalf("name = %q, want feature-workflow", wf.Name)
	}
	wantApplies := []string{"feature", "fix"}
	if len(wf.AppliesTo) != len(wantApplies) {
		t.Fatalf("applies_to = %v, want %v", wf.AppliesTo, wantApplies)
	}
	for i, want := range wantApplies {
		if wf.AppliesTo[i] != want {
			t.Fatalf("applies_to[%d] = %q, want %q", i, wf.AppliesTo[i], want)
		}
	}
	wantStates := []string{"backlog", "planning", "planned", "in-progress", "completed"}
	if len(wf.States) != len(wantStates) {
		t.Fatalf("states = %v, want %v", wf.States, wantStates)
	}
	for i, want := range wantStates {
		if wf.States[i].Name != want {
			t.Fatalf("states[%d] = %q, want %q", i, wf.States[i].Name, want)
		}
	}
	if len(wf.Transitions) != 4 {
		t.Fatalf("transitions count = %d, want 4", len(wf.Transitions))
	}
	if got, ok := wf.FindTransition("planning", "planned"); !ok || got.ReviewerSkill != "plan-review" {
		t.Fatalf("planning→planned = %+v ok=%v, want plan-review", got, ok)
	}
	if got, ok := wf.FindTransition("in-progress", "completed"); !ok || got.ReviewerSkill != "done-review" {
		t.Fatalf("in-progress→completed = %+v ok=%v, want done-review", got, ok)
	}
}

func TestTransitionsFrom(t *testing.T) {
	wf, err := Parse([]byte(exampleSkill))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := wf.TransitionsFrom("backlog"); len(got) != 1 || got[0].To != "planning" {
		t.Fatalf("from backlog: %+v", got)
	}
	if got := wf.TransitionsFrom("completed"); len(got) != 0 {
		t.Fatalf("terminal state should have 0 outgoing transitions, got %+v", got)
	}
	if got := wf.TransitionsFrom("unknown-state"); len(got) != 0 {
		t.Fatalf("unknown state should yield 0 transitions, got %+v", got)
	}
}

func TestParse_MalformedShapes(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantSub string
	}{
		{
			name:    "missing frontmatter",
			input:   "# just markdown\n",
			wantSub: "name required",
		},
		{
			name: "frontmatter unclosed",
			input: `---
name: incomplete
# no closing delimiter
`,
			wantSub: "without matching closing",
		},
		{
			name: "applies_to missing",
			input: `---
name: x
---

` + "```yaml" + `
states: [a]
transitions: [{from: a, to: a}]
` + "```" + `
`,
			wantSub: "applies_to required",
		},
		{
			name: "no yaml block in body",
			input: `---
name: x
applies_to: [feature]
---

# Free prose with no yaml block
`,
			wantSub: "no ```yaml block",
		},
		{
			name: "yaml block unclosed",
			input: `---
name: x
applies_to: [feature]
---

` + "```yaml" + `
states: [a]
transitions: [{from: a, to: a}]
`,
			wantSub: "opening ```yaml without closing",
		},
		{
			name: "transition references undeclared state",
			input: `---
name: x
applies_to: [feature]
---

` + "```yaml" + `
states: [a, b]
transitions:
  - {from: a, to: c}
` + "```" + `
`,
			wantSub: "transitions[0].to \"c\" is not a declared state",
		},
		{
			name: "duplicate states",
			input: `---
name: x
applies_to: [feature]
---

` + "```yaml" + `
states: [a, a]
transitions: [{from: a, to: a}]
` + "```" + `
`,
			wantSub: "states[1] \"a\" is duplicated",
		},
		{
			name: "duplicate transitions",
			input: `---
name: x
applies_to: [feature]
---

` + "```yaml" + `
states: [a, b]
transitions:
  - {from: a, to: b}
  - {from: a, to: b}
` + "```" + `
`,
			wantSub: "transitions[1] a->b is duplicated",
		},
		{
			name: "empty states",
			input: `---
name: x
applies_to: [feature]
---

` + "```yaml" + `
states: []
transitions: []
` + "```" + `
`,
			wantSub: "states required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.input))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestParse_DynamicFlag(t *testing.T) {
	input := `---
name: x
applies_to: [feature]
---

` + "```yaml" + `
states: [a, b]
transitions:
  - {from: a, to: b, dynamic: true}
` + "```" + `
`
	wf, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !wf.Transitions[0].Dynamic {
		t.Fatalf("dynamic flag lost: %+v", wf.Transitions[0])
	}
}

// TestParse_WorkSkill covers epic:workflow-steps story 2 AC#1: a transition may
// declare an optional work_skill (the step's "do" half) alongside reviewer_skill,
// it round-trips through the parser, and a transition that omits it parses
// unchanged (empty WorkSkill) — existing workflows are not disturbed.
func TestParse_WorkSkill(t *testing.T) {
	input := `---
name: x
applies_to: [feature]
---

` + "```yaml" + `
states: [a, b, c]
transitions:
  - {from: a, to: b, work_skill: commit-push, reviewer_skill: "satellites-commit-push-review"}
  - {from: b, to: c, reviewer_skill: "done-review"}
` + "```" + `
`
	wf, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	step, ok := wf.FindTransition("a", "b")
	if !ok {
		t.Fatalf("transition a->b missing")
	}
	if step.WorkSkill != "commit-push" {
		t.Fatalf("work_skill lost: got %q want %q (%+v)", step.WorkSkill, "commit-push", step)
	}
	if step.ReviewerSkill != "satellites-commit-push-review" {
		t.Fatalf("reviewer_skill lost alongside work_skill: %+v", step)
	}
	bare, ok := wf.FindTransition("b", "c")
	if !ok {
		t.Fatalf("transition b->c missing")
	}
	if bare.WorkSkill != "" {
		t.Fatalf("a transition with no work_skill must parse with empty WorkSkill, got %q", bare.WorkSkill)
	}
}

// sty_028c3f92 removed IsCommitStep (the `work_skill == "commit-push"` literal
// that bound git push to a ship step). The `work_skill` FIELD still parses
// generically (see the work_skill round-trip test above); only the value-keyed
// predicate is gone — the binary no longer asserts which step may push.

// TestParse_V2Sketch parses the graduated-workflow target model (the epic
// anchor's sketch, verbatim) and asserts the v2 surface: state actors,
// on:pass|fail edges, iteration bounds, exhaustion targets, and the
// checkpoint trigger — epic:graduated-workflow story 1 AC#1.
func TestParse_V2Sketch(t *testing.T) {
	body := []byte("# v2 sketch\n\n```yaml\n" +
		"states:\n" +
		"  - {name: in_progress,     actor: executor}\n" +
		"  - {name: techdebt-review, actor: satellites}\n" +
		"  - {name: done-review,     actor: reviewer}\n" +
		"  - {name: blocked,         actor: operator}\n" +
		"  - done\n" +
		"transitions:\n" +
		"  - {from: in_progress,     to: techdebt-review, trigger: checkpoint}\n" +
		"  - {from: techdebt-review, on: pass, to: done-review}\n" +
		"  - {from: techdebt-review, on: fail, to: in_progress, max_iterations: 3, on_exhausted: blocked}\n" +
		"  - {from: done-review,     on: pass, to: done}\n" +
		"  - {from: done-review,     on: fail, to: in_progress, max_iterations: 3, on_exhausted: blocked}\n" +
		"```\n")
	wf, err := ParseBody(body)
	if err != nil {
		t.Fatalf("parse v2 sketch: %v", err)
	}
	wantActors := map[string]string{
		"in_progress": "executor", "techdebt-review": "satellites",
		"done-review": "reviewer", "blocked": "operator", "done": "",
	}
	for state, actor := range wantActors {
		if got := wf.ActorOf(state); got != actor {
			t.Errorf("ActorOf(%q) = %q, want %q", state, got, actor)
		}
	}
	if len(wf.Transitions) != 5 {
		t.Fatalf("transitions = %d, want 5", len(wf.Transitions))
	}
	if tr := wf.Transitions[0]; tr.Trigger != "checkpoint" || tr.On != "" {
		t.Errorf("checkpoint edge parsed wrong: %+v", tr)
	}
	fail := wf.Transitions[2]
	if fail.On != "fail" || fail.MaxIterations != 3 || fail.OnExhausted != "blocked" {
		t.Errorf("fail edge parsed wrong: %+v", fail)
	}
	if pass := wf.Transitions[3]; pass.On != "pass" || pass.MaxIterations != 0 || pass.OnExhausted != "" {
		t.Errorf("pass edge parsed wrong: %+v", pass)
	}
	if err := wf.ValidateLifecycle(); err != nil {
		t.Errorf("v2 sketch must pass ValidateLifecycle: %v", err)
	}
	// blocked and done are both terminal; the loop edges keep in_progress non-terminal.
	terms := wf.TerminalStates()
	if len(terms) != 2 {
		t.Errorf("terminal states = %v, want [blocked done]", terms)
	}
}

// TestParse_V2Rejections pins every malformed v2 shape the validator must
// reject — epic:graduated-workflow story 1 AC#3.
func TestParse_V2Rejections(t *testing.T) {
	wrap := func(yaml string) []byte { return []byte("# t\n\n```yaml\n" + yaml + "```\n") }
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "unbounded fail-cycle",
			yaml: "states: [a, b, done]\ntransitions:\n" +
				"  - {from: a, to: b}\n" +
				"  - {from: b, on: pass, to: done}\n" +
				"  - {from: b, on: fail, to: a}\n",
			want: "closes a cycle with no max_iterations",
		},
		{
			name: "on_exhausted names undeclared state",
			yaml: "states: [a, b, done]\ntransitions:\n" +
				"  - {from: a, to: b}\n" +
				"  - {from: b, on: pass, to: done}\n" +
				"  - {from: b, on: fail, to: a, max_iterations: 3, on_exhausted: limbo}\n",
			want: "on_exhausted \"limbo\" is not a declared state",
		},
		{
			name: "dangling pass without fail",
			yaml: "states: [a, b, done]\ntransitions:\n" +
				"  - {from: a, to: b}\n" +
				"  - {from: b, on: pass, to: done}\n",
			want: "no `on: fail` edge",
		},
		{
			name: "dangling fail without pass",
			yaml: "states: [a, b, done]\ntransitions:\n" +
				"  - {from: a, to: b}\n" +
				"  - {from: b, on: fail, to: done}\n",
			want: "no `on: pass` edge",
		},
		{
			name: "max_iterations on a pass edge",
			yaml: "states: [a, b, done]\ntransitions:\n" +
				"  - {from: a, to: b}\n" +
				"  - {from: b, on: pass, to: done, max_iterations: 3}\n" +
				"  - {from: b, on: fail, to: a, max_iterations: 3, on_exhausted: done}\n",
			want: "not an `on: fail` edge",
		},
		{
			name: "max_iterations without on_exhausted",
			yaml: "states: [a, b, done]\ntransitions:\n" +
				"  - {from: a, to: b}\n" +
				"  - {from: b, on: pass, to: done}\n" +
				"  - {from: b, on: fail, to: a, max_iterations: 3}\n",
			want: "no on_exhausted",
		},
		{
			name: "on_exhausted without max_iterations",
			yaml: "states: [a, b, done]\ntransitions:\n" +
				"  - {from: a, to: b}\n" +
				"  - {from: b, on: pass, to: done}\n" +
				"  - {from: b, on: fail, to: a, on_exhausted: done}\n",
			want: "no max_iterations",
		},
		{
			name: "unknown on value",
			yaml: "states: [a, done]\ntransitions:\n" +
				"  - {from: a, to: done, on: maybe}\n",
			want: "not pass or fail",
		},
		{
			name: "unknown trigger value",
			yaml: "states: [a, done]\ntransitions:\n" +
				"  - {from: a, to: done, trigger: cron}\n",
			want: "not checkpoint",
		},
		{
			name: "blank actor",
			yaml: "states:\n  - {name: a, actor: \"  \"}\n  - done\ntransitions:\n" +
				"  - {from: a, to: done}\n",
			want: "blank actor",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseBody(wrap(c.yaml))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), c.want)
			}
		})
	}
}

// TestParse_V2BoundedLoopAccepted: a fail edge that closes a cycle WITH a
// bound is legal — the rejection above is specifically about unbounded loops.
// A bounded fail edge pointing at a non-cycling target is equally fine.
func TestParse_V2BoundedLoopAccepted(t *testing.T) {
	body := []byte("# t\n\n```yaml\n" +
		"states: [a, b, blocked, done]\n" +
		"transitions:\n" +
		"  - {from: a, to: b}\n" +
		"  - {from: b, on: pass, to: done}\n" +
		"  - {from: b, on: fail, to: a, max_iterations: 2, on_exhausted: blocked}\n" +
		"```\n")
	wf, err := ParseBody(body)
	if err != nil {
		t.Fatalf("bounded loop must parse: %v", err)
	}
	if err := wf.ValidateLifecycle(); err != nil {
		t.Fatalf("bounded loop must pass lifecycle: %v", err)
	}
}

// TestParse_V2LegacyMixUnchanged: reviewer_skill edges with no `on` keep
// accept-moves/reject-stays semantics — PickTransition still resolves the
// single gated edge, and the v2 fields zero-value cleanly.
func TestParse_V2LegacyMixUnchanged(t *testing.T) {
	body := []byte("# t\n\n```yaml\n" +
		"states: [backlog, in_progress, done]\n" +
		"transitions:\n" +
		"  - {from: backlog, to: in_progress, reviewer_skill: \"plan\"}\n" +
		"  - {from: in_progress, to: done, reviewer_skill: \"close\"}\n" +
		"```\n")
	wf, err := ParseBody(body)
	if err != nil {
		t.Fatalf("legacy workflow must parse: %v", err)
	}
	tr, gate, dyn, err := wf.PickTransition("backlog")
	if err != nil || dyn || gate != "plan" || tr.To != "in_progress" {
		t.Fatalf("legacy PickTransition changed: tr=%+v gate=%q dyn=%v err=%v", tr, gate, dyn, err)
	}
	for _, tr := range wf.Transitions {
		if tr.On != "" || tr.MaxIterations != 0 || tr.OnExhausted != "" || tr.Trigger != "" {
			t.Fatalf("legacy edge grew v2 fields: %+v", tr)
		}
	}
}

// TestParse_V2StateCommand pins the command rider: legal on an
// actor:satellites state, rejected anywhere else; a satellites state may
// omit it (the dispatcher refuses at advance time, not parse time).
func TestParse_V2StateCommand(t *testing.T) {
	good := []byte("# t\n\n```yaml\n" +
		"states:\n  - {name: a, actor: executor}\n  - {name: td, actor: satellites, command: \"satellites techdebt review\"}\n  - {name: nocmd, actor: satellites}\n  - done\n" +
		"transitions:\n  - {from: a, to: td}\n  - {from: td, on: pass, to: done}\n  - {from: td, on: fail, to: a, max_iterations: 2, on_exhausted: done}\n  - {from: nocmd, to: done}\n```\n")
	wf, err := ParseBody(good)
	if err != nil {
		t.Fatalf("command on satellites state must parse: %v", err)
	}
	st, _ := wf.StateOf("td")
	if st.Command != "satellites techdebt review" {
		t.Fatalf("command = %q", st.Command)
	}
	if st2, _ := wf.StateOf("nocmd"); st2.Command != "" {
		t.Fatalf("omitted command should be empty, got %q", st2.Command)
	}
	bad := []byte("# t\n\n```yaml\n" +
		"states:\n  - {name: a, actor: executor, command: \"make\"}\n  - done\n" +
		"transitions:\n  - {from: a, to: done}\n```\n")
	if _, err := ParseBody(bad); err == nil || !strings.Contains(err.Error(), "actor is not satellites") {
		t.Fatalf("command on non-satellites state must reject, got %v", err)
	}
}
