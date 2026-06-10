package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/frontmatter"
	"github.com/bobmcallan/satellites/internal/workflow"
)

// TestScaffoldWorkflowWhenAbsent pins AC1/AC4: a repo with no kind:workflow
// source gets the trunk workflow + gate templates written into
// .satellites/skills/, and the run reports them + the next step.
func TestScaffoldWorkflowWhenAbsent(t *testing.T) {
	satDir := filepath.Join(t.TempDir(), ".satellites")
	if err := os.MkdirAll(satDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var out bytes.Buffer
	if err := scaffoldWorkflowIfAbsent(&out, satDir); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	for _, want := range []string{
		"satellites-trunk-workflow.md",
		"satellites-trunk-plan-review.md",
		"satellites-trunk-done-review.md",
	} {
		if _, err := os.Stat(filepath.Join(satDir, "skills", want)); err != nil {
			t.Errorf("scaffold did not write %s: %v", want, err)
		}
		if !strings.Contains(out.String(), want) {
			t.Errorf("report missing %s\n%s", want, out.String())
		}
	}
	if !strings.Contains(out.String(), "skill upload") {
		t.Errorf("report missing next-step pointer:\n%s", out.String())
	}
	// The scaffold is now a workflow source — a second run is a no-op.
	has, err := hasWorkflowSource(filepath.Join(satDir, "skills"))
	if err != nil || !has {
		t.Fatalf("hasWorkflowSource after scaffold = %v, %v; want true,nil", has, err)
	}
}

// TestScaffoldNoOpWhenWorkflowPresent pins AC2: a repo that already declares a
// kind:workflow skill is left untouched — no scaffold files written.
func TestScaffoldNoOpWhenWorkflowPresent(t *testing.T) {
	satDir := filepath.Join(t.TempDir(), ".satellites")
	skills := filepath.Join(satDir, "skills")
	if err := os.MkdirAll(skills, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// An existing workflow source of any name.
	existing := "---\nname: my-flow\nkind: workflow\napplies_to: [feature]\ndescription: x\n---\n# flow\n"
	if err := os.WriteFile(filepath.Join(skills, "my-flow.md"), []byte(existing), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var out bytes.Buffer
	if err := scaffoldWorkflowIfAbsent(&out, satDir); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if _, err := os.Stat(filepath.Join(skills, "satellites-trunk-workflow.md")); !os.IsNotExist(err) {
		t.Errorf("scaffold wrote a file despite an existing workflow (err=%v)", err)
	}
	if !strings.Contains(out.String(), "scaffold skipped") {
		t.Errorf("report should note the skip:\n%s", out.String())
	}
}

// TestScaffoldNeverClobbers pins the non-clobber rule: an existing scaffold file
// is preserved verbatim even when the repo has no workflow yet.
func TestScaffoldNeverClobbers(t *testing.T) {
	satDir := filepath.Join(t.TempDir(), ".satellites")
	skills := filepath.Join(satDir, "skills")
	if err := os.MkdirAll(skills, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A non-workflow file colliding with a scaffold name (operator-edited).
	custom := "operator content — do not clobber\n"
	if err := os.WriteFile(filepath.Join(skills, "satellites-trunk-plan-review.md"), []byte(custom), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var out bytes.Buffer
	if err := scaffoldWorkflowIfAbsent(&out, satDir); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(skills, "satellites-trunk-plan-review.md"))
	if string(got) != custom {
		t.Errorf("clobbered an existing file: %q", got)
	}
}

// TestScaffoldTemplatesAreValid pins AC4 + AC5 (no-runtime-default is satisfied
// by SOURCE templates, so they must be valid + self-consistent): every embedded
// scaffold skill carries required frontmatter; the workflow's lifecycle validates
// and declares applies_to; and every reviewer_skill it names is one of the
// scaffolded gate skills (no missing-gate-skill at runtime).
func TestScaffoldTemplatesAreValid(t *testing.T) {
	satDir := filepath.Join(t.TempDir(), ".satellites")
	if err := os.MkdirAll(satDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := scaffoldWorkflowIfAbsent(&bytes.Buffer{}, satDir); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	skills := filepath.Join(satDir, "skills")
	entries, err := os.ReadDir(skills)
	if err != nil {
		t.Fatalf("read skills: %v", err)
	}

	names := map[string]bool{}
	var workflowReviewerSkills []string
	sawWorkflow := false
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(skills, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		fm, _, perr := frontmatter.Parse(raw)
		if perr != nil {
			t.Fatalf("%s: frontmatter parse: %v", e.Name(), perr)
		}
		if !strings.HasPrefix(fm.Name, "satellites-trunk-") {
			t.Errorf("%s: name %q should start with satellites-trunk-", e.Name(), fm.Name)
		}
		if strings.TrimSpace(fm.Kind) == "" || strings.TrimSpace(fm.Description) == "" {
			t.Errorf("%s: skill missing kind/description", e.Name())
		}
		names[fm.Name] = true

		if strings.TrimSpace(fm.Kind) == "workflow" {
			sawWorkflow = true
			if len(fm.AppliesTo) == 0 {
				t.Errorf("%s: workflow missing applies_to", e.Name())
			}
			wf, werr := workflow.Parse(raw)
			if werr != nil {
				t.Fatalf("%s: workflow parse: %v", e.Name(), werr)
			}
			if lerr := wf.ValidateLifecycle(); lerr != nil {
				t.Errorf("%s: lifecycle invalid: %v", e.Name(), lerr)
			}
			for _, tr := range wf.Transitions {
				if tr.ReviewerSkill != "" {
					workflowReviewerSkills = append(workflowReviewerSkills, tr.ReviewerSkill)
				}
			}
		}
	}
	if !sawWorkflow {
		t.Fatal("scaffold has no kind:workflow skill")
	}
	// Every gate the workflow names must be a scaffolded skill — otherwise a
	// fresh repo's first plan-review would reject with missing-gate-skill.
	for _, rs := range workflowReviewerSkills {
		if !names[rs] {
			t.Errorf("workflow reviewer_skill %q has no scaffolded gate (have %v)", rs, names)
		}
	}
}
