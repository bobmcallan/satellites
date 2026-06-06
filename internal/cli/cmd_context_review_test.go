package cli

import (
	"encoding/json"
	"testing"
)

const cleanWorkflowBody = "# Story\n\n## Workflow\n\n```yaml\nstates:\n  - backlog\n  - in_progress\n  - done\ntransitions:\n  - {from: backlog,     to: in_progress, reviewer_skill: \"satellites-story-plan-review\"}\n  - {from: in_progress, to: done,        reviewer_skill: \"satellites-story-done-review\"}\n```\n"

// allSkillsExist / noSkillsExist are skillExists stubs for the pure check.
func allSkillsExist(string) bool { return true }
func noSkillsExist(string) bool  { return false }

func TestReviewContextConflicts_Clean(t *testing.T) {
	got := reviewContextConflicts(cleanWorkflowBody, allSkillsExist)
	if len(got) != 0 {
		t.Fatalf("clean workflow with resolvable skills should yield 0 findings, got %v", got)
	}
}

func TestReviewContextConflicts_MissingGateSkill(t *testing.T) {
	got := reviewContextConflicts(cleanWorkflowBody, noSkillsExist)
	// both reviewer skills are absent -> two missing-gate-skill findings (deduped by name)
	if len(got) != 2 {
		t.Fatalf("expected 2 missing-gate-skill findings, got %d: %v", len(got), got)
	}
	for _, f := range got {
		if f.Code != "missing-gate-skill" {
			t.Errorf("unexpected code %q (want missing-gate-skill)", f.Code)
		}
	}
	// duplicate skill across edges reports once
	dupBody := "## Workflow\n```yaml\nstates: [a, b, c]\ntransitions:\n  - {from: a, to: b, reviewer_skill: \"dup\"}\n  - {from: b, to: c, reviewer_skill: \"dup\"}\n```\n"
	dg := reviewContextConflicts(dupBody, noSkillsExist)
	missing := 0
	for _, f := range dg {
		if f.Code == "missing-gate-skill" {
			missing++
		}
	}
	if missing != 1 {
		t.Errorf("duplicate reviewer_skill should report missing-gate-skill once, got %d (%v)", missing, dg)
	}
}

func TestReviewContextConflicts_NoTerminal(t *testing.T) {
	// every state has an outgoing edge -> a cycle -> ValidateLifecycle fails
	cyclic := "## Workflow\n```yaml\nstates: [a, b]\ntransitions:\n  - {from: a, to: b, reviewer_skill: \"s\"}\n  - {from: b, to: a, reviewer_skill: \"s\"}\n```\n"
	got := reviewContextConflicts(cyclic, allSkillsExist)
	found := false
	for _, f := range got {
		if f.Code == "workflow-lifecycle" {
			found = true
		}
	}
	if !found {
		t.Fatalf("cyclic workflow should yield workflow-lifecycle, got %v", got)
	}
}

func TestReviewContextConflicts_Unparseable(t *testing.T) {
	got := reviewContextConflicts("# Story with no workflow block\n", allSkillsExist)
	if len(got) != 1 || got[0].Code != "workflow-unparseable" {
		t.Fatalf("expected single workflow-unparseable finding, got %v", got)
	}
}

func TestConflictFindingJSONRoundTrip(t *testing.T) {
	in := []conflictFinding{{Severity: "error", Code: "missing-gate-skill", Message: "x"}}
	raw, err := json.Marshal(map[string]any{"story_id": "sty_x", "findings": in})
	if err != nil {
		t.Fatal(err)
	}
	var back struct {
		StoryID  string            `json:"story_id"`
		Findings []conflictFinding `json:"findings"`
	}
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.StoryID != "sty_x" || len(back.Findings) != 1 || back.Findings[0].Code != "missing-gate-skill" {
		t.Fatalf("round-trip mismatch: %+v", back)
	}
}
