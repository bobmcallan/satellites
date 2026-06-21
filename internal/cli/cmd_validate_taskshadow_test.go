package cli

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/workflow"
)

// wfBody builds a minimal valid workflow .md (frontmatter applies_to + a
// ## Workflow yaml block with a linear state chain) for the shadow check.
func wfBody(name string, appliesTo, states []string) string {
	var b strings.Builder
	b.WriteString("---\nname: " + name + "\nkind: workflow\napplies_to: [")
	for i, a := range appliesTo {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("\"" + a + "\"")
	}
	b.WriteString("]\n---\n\n## Workflow\n\n```yaml\nstates:\n")
	for _, s := range states {
		b.WriteString("  - " + s + "\n")
	}
	b.WriteString("transitions:\n")
	for i := 0; i+1 < len(states); i++ {
		b.WriteString("  - {from: " + states[i] + ", to: " + states[i+1] + ", reviewer_skill: \"r\"}\n")
	}
	b.WriteString("```\n")
	return b.String()
}

// TestCheckTaskWorkflowShadows pins sty_d3fead0d: a story-shaped workflow that
// claims applies_to:[task] is flagged (block) as shadowing the canonical task
// workflow; a genuinely task-shaped workflow, and a story-only one, are not.
func TestCheckTaskWorkflowShadows(t *testing.T) {
	story := wfBody("vire-release", []string{"task", "story"}, []string{"backlog", "in_progress", "done"})
	taskwf := wfBody("satellites-task-workflow", []string{"task"}, []string{"ready", "running", "complete"})
	storyOnly := wfBody("story-wf", []string{"story"}, []string{"backlog", "in_progress", "done"})

	findings := checkTaskWorkflowShadows([]matSkill{
		{name: "vire-release", raw: story},
		{name: "satellites-task-workflow", raw: taskwf},
		{name: "story-wf", raw: storyOnly},
	})

	if len(findings) != 1 {
		t.Fatalf("want exactly 1 shadow finding, got %d: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.Artifact != "vire-release" || f.Severity != "block" || f.Code != "task-workflow-shadow" {
		t.Fatalf("unexpected shadow finding: %+v", f)
	}
	if !strings.Contains(f.Message, "applies_to") {
		t.Errorf("finding should name the applies_to fix: %q", f.Message)
	}
}

// TestTaskShapedWorkflow: the lifecycle test recognises ready→running→complete
// and rejects a story/deploy shape.
func TestTaskShapedWorkflow(t *testing.T) {
	for _, tc := range []struct {
		states []string
		want   bool
	}{
		{[]string{"ready", "running", "complete"}, true},
		{[]string{"ready", "running", "complete", "cancelled"}, true},
		{[]string{"backlog", "in_progress", "done"}, false},
		{[]string{"ready", "running"}, false}, // missing complete
	} {
		wf, err := workflow.Parse([]byte(wfBody("w", []string{"task"}, tc.states)))
		if err != nil {
			t.Fatalf("parse %v: %v", tc.states, err)
		}
		if got := taskShapedWorkflow(wf); got != tc.want {
			t.Errorf("taskShapedWorkflow(%v) = %v, want %v", tc.states, got, tc.want)
		}
	}
}
