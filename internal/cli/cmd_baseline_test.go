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
// kind:workflow, passes ValidateLifecycle, is applies_to ["*"], carries the
// backlog→in_progress→done lifecycle, and wires the INTENT SPINE — the entry
// (backlog→in_progress) is gated by the embedded internal intent-gate while the
// exit stays ungated (epic:satellites-backbone 2.4.2).
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
	entryGated, exitUngated := false, false
	for _, tr := range wf.Transitions {
		switch {
		case tr.From == "backlog" && tr.To == "in_progress":
			entryGated = strings.TrimSpace(tr.ReviewerSkill) == "satellites-intent-plan-review"
		case tr.From == "in_progress" && tr.To == "done":
			exitUngated = strings.TrimSpace(tr.ReviewerSkill) == ""
		}
	}
	if !entryGated {
		t.Errorf("baseline entry backlog→in_progress must be gated by satellites-intent-plan-review")
	}
	if !exitUngated {
		t.Errorf("baseline exit in_progress→done must stay ungated")
	}
	// The intent-gate must be an embedded internal gate — a clean repo with no
	// materialised skills must still be able to run it.
	if !verb.IsInternalGate("satellites-intent-plan-review") {
		t.Errorf("the baseline's entry gate must be an embedded internal gate")
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

// TestEnsureStarterConstitution: scaffolds the starter constitution into an
// empty repo; create-if-absent (a repo that already owns any principle is
// untouched, so no second resident constitution) (epic:satellites-backbone 2.4).
func TestEnsureStarterConstitution(t *testing.T) {
	repo := t.TempDir()
	added, err := ensureStarterConstitution(repo)
	if err != nil || !added {
		t.Fatalf("fresh ensureStarterConstitution: added=%v err=%v", added, err)
	}
	b, err := os.ReadFile(filepath.Join(repo, ".satellites", "principles", "constitution.md"))
	if err != nil {
		t.Fatalf("constitution not written: %v", err)
	}
	if !strings.Contains(string(b), "principles:always") || !strings.Contains(string(b), "configuration, not code") {
		t.Errorf("starter constitution missing the resident tag / config-over-code rule")
	}
	if added, _ := ensureStarterConstitution(repo); added {
		t.Errorf("re-run scaffolded again — must be create-if-absent")
	}

	// A repo that already owns a (differently-named) principle is left untouched.
	repo2 := t.TempDir()
	dir := filepath.Join(repo2, ".satellites", "principles")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "my-constitution.md"), []byte("# mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if added, _ := ensureStarterConstitution(repo2); added {
		t.Errorf("scaffolded over a repo that already owns a principle")
	}
	if _, err := os.Stat(filepath.Join(dir, "constitution.md")); !os.IsNotExist(err) {
		t.Errorf("constitution written despite an existing principle")
	}
}

// TestBaselineWorkflowDoc_WorkflowCheckClean: the baseline names the internal
// intent-gates (never materialised), yet the drift checks raise no blocking
// finding — proving 2.4.1 recognition covers the order-zero baseline (2.4.2).
func TestBaselineWorkflowDoc_WorkflowCheckClean(t *testing.T) {
	wf := matSkill{name: "satellites-baseline-workflow", kind: "workflow", description: "the order-zero baseline",
		body: baselineWorkflowDoc, raw: baselineWorkflowDoc}
	for _, f := range runWorkflowChecks([]matSkill{wf}, nil, nil) {
		if f.Severity == "block" {
			t.Errorf("baseline naming internal intent-gates must be workflow-check CLEAN, got: %+v", f)
		}
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
	if setStatusAllowed("fix", "backlog", "in_progress", "", baseline) {
		t.Errorf("the baseline entry is now gated by the intent-gate — set-status must refuse it")
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
