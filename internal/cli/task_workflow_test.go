package cli

import (
	"io/fs"
	"testing"

	substrate "github.com/bobmcallan/satellites/config"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workflow"
)

// TestTaskWorkflowEmbeddedAndGatesResolve pins the satellites-task-workflow
// substrate (epic:project-tasks): it ships in the binary embed, applies to
// category "task", declares the ready→running→complete (+cancel) lifecycle, and
// names only reviewer gates that resolve from the embed (verb.IsConfigSkill).
// This guards against a rename/removal silently breaking the task lifecycle.
func TestTaskWorkflowEmbeddedAndGatesResolve(t *testing.T) {
	raw, err := fs.ReadFile(substrate.FS, "workflows/satellites-task-workflow.md")
	if err != nil {
		t.Fatalf("satellites-task-workflow.md not embedded: %v", err)
	}
	wf, err := workflow.Parse(raw)
	if err != nil || wf == nil {
		t.Fatalf("satellites-task-workflow does not parse: %v", err)
	}

	// applies_to includes "task" so a category='task' row resolves it.
	if !containsString(wf.AppliesTo, "task") {
		t.Errorf("applies_to = %v, want it to contain \"task\"", wf.AppliesTo)
	}

	// Every forward + cancel gate must be a satellites-task-* reviewer that
	// resolves from the binary embed.
	wantGates := map[string]bool{
		"satellites-task-upsert-review": false,
		"satellites-task-report-review": false,
		"satellites-task-cancel-review": false,
	}
	for _, tr := range wf.Transitions {
		g := tr.ReviewerSkill
		if g == "" {
			continue
		}
		if !verb.IsConfigSkill(g) {
			t.Errorf("transition %s→%s names gate %q that does not resolve from the embed", tr.From, tr.To, g)
		}
		if _, ok := wantGates[g]; ok {
			wantGates[g] = true
		}
	}
	for g, seen := range wantGates {
		if !seen {
			t.Errorf("expected the task workflow to name gate %q", g)
		}
	}

	// The forward lifecycle reaches the terminal "complete" state.
	if !containsTransition(wf, "ready", "running") ||
		!containsTransition(wf, "running", "complete") {
		t.Errorf("task workflow must declare ready→running and running→complete; got %+v", wf.Transitions)
	}
}

func containsString(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func containsTransition(wf *workflow.Workflow, from, to string) bool {
	for _, tr := range wf.Transitions {
		if tr.From == from && tr.To == to {
			return true
		}
	}
	return false
}
