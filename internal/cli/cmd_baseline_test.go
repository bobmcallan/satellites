package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	substrate "github.com/bobmcallan/satellites/config"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workflow"
)

// baselineWorkflowDoc is the embedded order-zero baseline workflow body — the
// GOVERNANCE SOURCE in the binary (config/workflows), no longer scaffolded into a
// repo (sty_a69e8c61). Read here so the baseline-shape tests below assert against
// the authoritative embed.
var baselineWorkflowDoc = string(substrate.BaselineWorkflowMarkdown())

// TestInitNoWorkflowScaffold pins sty_a69e8c61: `satellites init` NO LONGER
// scaffolds a baseline/parent workflow into .satellites/workflows/ — the embed is
// the governance source, so a fresh repo is governed with no scaffolded copy (no
// embed↔.satellites drift). It still scaffolds the starter constitution.
func TestInitNoWorkflowScaffold(t *testing.T) {
	repo := t.TempDir()
	var out bytes.Buffer
	if err := runInit(&out, repo); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	for _, name := range []string{"satellites-baseline-workflow.md", "satellites-parent-workflow.md"} {
		if _, err := os.Stat(filepath.Join(repo, ".satellites", "workflows", name)); !os.IsNotExist(err) {
			t.Errorf("init must NOT scaffold %s (the embed governs), but it exists", name)
		}
	}
	// The starter constitution is still scaffolded.
	if _, err := os.Stat(filepath.Join(repo, ".satellites", "principles", "constitution.md")); err != nil {
		t.Errorf("init must still scaffold the starter constitution: %v", err)
	}
}

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
	entryGated, exitGated := false, false
	for _, tr := range wf.Transitions {
		switch {
		case tr.From == "backlog" && tr.To == "in_progress":
			entryGated = strings.TrimSpace(tr.ReviewerSkill) == "satellites-intent-plan-review"
		case tr.From == "in_progress" && tr.To == "done":
			exitGated = strings.TrimSpace(tr.ReviewerSkill) == "satellites-story-done-review"
		}
	}
	if !entryGated {
		t.Errorf("baseline entry backlog→in_progress must be gated by satellites-intent-plan-review")
	}
	if !exitGated {
		t.Errorf("baseline exit in_progress→done must be gated by satellites-story-done-review (epic:system-substrate)")
	}
	// Entry and exit reviewers are both binary-resident config/skills reviewers —
	// they run in a clean repo with no materialised skills (resolved from the embed).
	if !verb.IsConfigSkill("satellites-intent-plan-review") {
		t.Errorf("the baseline's entry reviewer must be a binary-resident config/skills reviewer")
	}
	if !verb.IsConfigSkill("satellites-story-done-review") {
		t.Errorf("the baseline's exit reviewer must be a binary-resident config/skills reviewer")
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
	// The starter ships effectively BLANK — resident frontmatter (name +
	// principles:always) plus guidance to complete it, NOT satellites' own opinion.
	if !strings.Contains(string(b), "principles:always") || !strings.Contains(string(b), "name: constitution") {
		t.Errorf("starter constitution missing the resident frontmatter (name: constitution, principles:always)")
	}
	if !strings.Contains(string(b), "complete it") {
		t.Errorf("starter constitution should ship blank with guidance to complete it")
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

	if !setStatusAllowed("", "parent", "done", "backlog", "", nil) {
		t.Errorf("parent reopen must be allowed")
	}
	if setStatusAllowed("", "fix", "backlog", "in_progress", "", baseline) {
		t.Errorf("the baseline entry is now gated by the intent-gate — set-status must refuse it")
	}
	if setStatusAllowed("", "fix", "in_progress", "done", "", baseline) {
		t.Errorf("the baseline exit is now gated by satellites-story-done-review — set-status must refuse it")
	}
	if setStatusAllowed("", "fix", "backlog", "done", "", baseline) {
		t.Errorf("undeclared backlog→done must be refused")
	}

	gated := []verb.WorkflowSource{{Name: "gated", Body: gatedTestWorkflow}}
	if setStatusAllowed("", "fix", "backlog", "ready", "", gated) {
		t.Errorf("gated backlog→ready must be refused (it goes through the gate)")
	}
}
