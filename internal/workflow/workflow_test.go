package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	if _, ok := wf.FindTransition("in-progress", "completed"); !ok {
		t.Fatalf("expected in-progress→completed in example file")
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
		if wf.States[i] != want {
			t.Fatalf("states[%d] = %q, want %q", i, wf.States[i], want)
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
