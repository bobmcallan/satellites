package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplaceSection(t *testing.T) {
	body := "# Story\n\n## Plan\nplan text\n\n## Workflow\n\n```yaml\nold\n```\n\n## Context\ntail\n"
	out := replaceSection(body, "## Workflow", "## Workflow\n\n```yaml\nnew\n```")
	if strings.Contains(out, "old") {
		t.Fatalf("old workflow not replaced:\n%s", out)
	}
	if !strings.Contains(out, "new") {
		t.Fatalf("new workflow not present:\n%s", out)
	}
	if !strings.Contains(out, "## Plan") || !strings.Contains(out, "## Context") || !strings.Contains(out, "tail") {
		t.Fatalf("surrounding sections lost:\n%s", out)
	}
	// the Context section must survive AFTER the replaced Workflow
	if strings.Index(out, "## Workflow") > strings.Index(out, "## Context") {
		t.Fatalf("section order broken:\n%s", out)
	}
}

func TestReplaceSectionInsertWhenAbsent(t *testing.T) {
	body := "# Story\n\n## Plan\nonly a plan\n"
	out := replaceSection(body, "## Workflow", "## Workflow\n\n```yaml\nx\n```")
	if !strings.Contains(out, "## Workflow") || !strings.Contains(out, "x") {
		t.Fatalf("workflow not appended:\n%s", out)
	}
	if !strings.Contains(out, "only a plan") {
		t.Fatalf("existing body lost:\n%s", out)
	}
}

func TestReplaceSectionStripWhenEmpty(t *testing.T) {
	body := "# Story\n\n## Workflow\n\n```yaml\nx\n```\n"
	out := replaceSection(body, "## Workflow", "")
	if strings.Contains(out, "## Workflow") || strings.Contains(out, "x") {
		t.Fatalf("workflow not stripped:\n%s", out)
	}
}

func TestParseDesignOutput(t *testing.T) {
	bare := `{"recommended":0,"proposals":[{"rationale":"fits","workflow":"yaml"}]}`
	o, err := parseDesignOutput(bare)
	if err != nil || len(o.Proposals) != 1 || o.Proposals[0].Rationale != "fits" {
		t.Fatalf("bare parse: %+v err=%v", o, err)
	}
	fenced := "Here is the design:\n```json\n" + bare + "\n```\nDone."
	o2, err := parseDesignOutput(fenced)
	if err != nil || len(o2.Proposals) != 1 {
		t.Fatalf("fenced parse: %+v err=%v", o2, err)
	}
	if _, err := parseDesignOutput("no json here"); err == nil {
		t.Fatal("expected error for no-JSON output")
	}
}

const goodProposalWorkflow = "```yaml\nstates:\n  - backlog\n  - in_progress\n  - done\ntransitions:\n  - {from: backlog, to: in_progress, reviewer_skill: \"satellites-story-plan-review\"}\n  - {from: in_progress, to: done, reviewer_skill: \"satellites-story-done-review\"}\n```"

func TestProposalValidationViaReviewContextConflicts(t *testing.T) {
	// a sound proposal, all gate skills present → valid (0 findings)
	if f := reviewContextConflicts(goodProposalWorkflow, allSkillsExist); len(f) != 0 {
		t.Fatalf("sound proposal should be valid, got %v", f)
	}
	// a proposal whose reviewer_skill does not resolve → invalid
	f := reviewContextConflicts(goodProposalWorkflow, noSkillsExist)
	if len(f) == 0 {
		t.Fatal("proposal with unresolved gate skills should be invalid")
	}
	// a no-terminal (cyclic) proposal → workflow-lifecycle finding
	cyclic := "```yaml\nstates: [a, b]\ntransitions:\n  - {from: a, to: b, reviewer_skill: \"satellites-story-plan-review\"}\n  - {from: b, to: a, reviewer_skill: \"satellites-story-plan-review\"}\n```"
	hasLifecycle := false
	for _, x := range reviewContextConflicts(cyclic, allSkillsExist) {
		if x.Code == "workflow-lifecycle" {
			hasLifecycle = true
		}
	}
	if !hasLifecycle {
		t.Fatal("cyclic proposal should yield workflow-lifecycle")
	}
}

// TestAvailableGateSkillsKindFiltered pins the sty_c3f126cb atomicity
// contract: available_gate_skills is selected by the declared dispatch
// contract (frontmatter kind: gate), not by name. A true gate without
// "review" in its name is offered; a capability named *-review is not.
func TestAvailableGateSkillsKindFiltered(t *testing.T) {
	dir := t.TempDir()
	write := func(name, kind, desc string) {
		d := filepath.Join(dir, ".claude", "skills", name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: " + name + "\ntype: skill\nkind: " + kind + "\ndescription: " + desc + "\n---\n# " + name + "\n"
		if err := os.WriteFile(filepath.Join(d, "SKILL.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("techdebt-gate", "gate", "fail-closed pre-commit gate")
	write("plan-review", "gate", "entry gate")
	write("skill-review", "capability", "a capability that merely reviews artifacts")
	write("commit-push", "capability", "checkpoint capability")

	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)

	got := availableGateSkills()
	names := map[string]bool{}
	for _, g := range got {
		names[g["name"]] = true
	}
	if !names["techdebt-gate"] || !names["plan-review"] {
		t.Errorf("kind:gate skills must be offered, got %v", names)
	}
	if names["skill-review"] || names["commit-push"] {
		t.Errorf("non-gate skills must not be offered, got %v", names)
	}
}
