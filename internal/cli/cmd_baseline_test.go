package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workflow"
)

// TestBaselineWorkflowDoc: the scaffolded baseline parses as a valid
// kind:workflow, passes ValidateLifecycle, is applies_to ["*"], names NO gate
// (every edge ungated), and carries the backlog→in_progress→done lifecycle.
func TestBaselineWorkflowDoc(t *testing.T) {
	wf, err := workflow.Parse([]byte(baselineWorkflowDoc))
	if err != nil || wf == nil {
		t.Fatalf("baseline workflow does not parse: %v", err)
	}
	if err := wf.ValidateLifecycle(); err != nil {
		t.Fatalf("baseline workflow is degenerate: %v", err)
	}
	wild := false
	for _, at := range wf.AppliesTo {
		if strings.TrimSpace(at) == "*" {
			wild = true
		}
	}
	if !wild {
		t.Errorf("baseline must be applies_to [\"*\"], got %v", wf.AppliesTo)
	}
	for _, tr := range wf.Transitions {
		if strings.TrimSpace(tr.ReviewerSkill) != "" {
			t.Errorf("baseline must name no gate, but %s->%s has reviewer_skill %q", tr.From, tr.To, tr.ReviewerSkill)
		}
	}
	if !baselineHasEdge(wf, "backlog", "in_progress") || !baselineHasEdge(wf, "in_progress", "done") {
		t.Errorf("baseline missing the backlog→in_progress→done lifecycle edges")
	}
}

func baselineHasEdge(wf *workflow.Workflow, from, to string) bool {
	for _, t := range wf.Transitions {
		if t.From == from && t.To == to {
			return true
		}
	}
	return false
}

// TestEnsureBaselineWorkflow: scaffolds into an empty repo; create-if-absent
// (no-op when any workflow already exists, so an owning repo is untouched).
func TestEnsureBaselineWorkflow(t *testing.T) {
	repo := t.TempDir()
	added, err := ensureBaselineWorkflow(repo)
	if err != nil || !added {
		t.Fatalf("fresh ensureBaselineWorkflow: added=%v err=%v", added, err)
	}
	p := filepath.Join(repo, ".satellites", "workflows", "satellites-baseline-workflow.md")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("baseline not written: %v", err)
	}
	if added, _ := ensureBaselineWorkflow(repo); added {
		t.Errorf("re-run scaffolded again — must be create-if-absent")
	}

	// A repo that already owns a DIFFERENT workflow is left untouched.
	repo2 := t.TempDir()
	wfDir := filepath.Join(repo2, ".satellites", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, "my-workflow.md"), []byte("# mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if added, _ := ensureBaselineWorkflow(repo2); added {
		t.Errorf("scaffolded over a repo that already owns a workflow")
	}
	if _, err := os.Stat(filepath.Join(wfDir, "satellites-baseline-workflow.md")); !os.IsNotExist(err) {
		t.Errorf("baseline written despite an existing workflow")
	}
}

// gatedTestWorkflow is a minimal GATED workflow fixture: its forward edges carry
// reviewer skills, so set-status must refuse them.
const gatedTestWorkflow = `---
name: gated
kind: workflow
applies_to: ["*"]
---
## Workflow
` + "```yaml" + `
states:
  - backlog
  - ready
  - done
transitions:
  - {from: backlog, to: ready, reviewer_skill: "some-plan-gate"}
  - {from: ready, to: done, reviewer_skill: "some-done-gate"}
` + "```" + `
`

// TestSetStatusAllowed: a parent reopen is always allowed; a gateless-baseline
// advance along an ungated edge is allowed; a gated edge or an undeclared edge
// is refused (gated work goes through the reviewer gate).
func TestSetStatusAllowed(t *testing.T) {
	baseline := []verb.WorkflowSource{{Name: "satellites-baseline-workflow", Body: baselineWorkflowDoc}}

	if !setStatusAllowed("parent", "done", "backlog", "", nil) {
		t.Errorf("parent reopen must be allowed")
	}
	if !setStatusAllowed("fix", "backlog", "in_progress", "", baseline) {
		t.Errorf("ungated backlog→in_progress must be allowed under the baseline")
	}
	if !setStatusAllowed("fix", "in_progress", "done", "", baseline) {
		t.Errorf("ungated in_progress→done must be allowed under the baseline")
	}
	if setStatusAllowed("fix", "backlog", "done", "", baseline) {
		t.Errorf("undeclared backlog→done must be refused")
	}

	gated := []verb.WorkflowSource{{Name: "gated", Body: gatedTestWorkflow}}
	if setStatusAllowed("fix", "backlog", "ready", "", gated) {
		t.Errorf("gated backlog→ready must be refused (it goes through the gate)")
	}
}
