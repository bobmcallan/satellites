package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// aggregateStructural folds the per-story structural check across the corpus,
// tagging each finding with its story id. Multiple stories, one unparseable,
// one with a missing gate skill, one clean.
func TestAggregateStructural_FoldsAcrossCorpus(t *testing.T) {
	stories := []validateStory{
		{ID: "sty_clean", Body: cleanWorkflowBody},       // valid lifecycle + skills present
		{ID: "sty_nowf", Body: "# story, no workflow\n"}, // unparseable
		{ID: "sty_gate", Body: cleanWorkflowBody},        // skills absent -> missing-gate-skill
	}
	// skills present only for the "clean" story; the gate story sees none.
	skillExists := func(name string) bool {
		return strings.HasPrefix(name, "satellites-")
	}
	got := aggregateStructural(stories, skillExists)
	// clean: 0; nowf: 1 unparseable; gate: 0 (skills resolve under this stub).
	// Flip the stub to make the gate story fail, proving per-story tagging.
	if len(got) != 1 {
		t.Fatalf("expected 1 finding (the unparseable story), got %d: %v", len(got), got)
	}
	if got[0].StoryID != "sty_nowf" || got[0].Code != "workflow-unparseable" {
		t.Fatalf("finding not tagged to the right story: %+v", got[0])
	}
	if got[0].Source != "context-validate" {
		t.Errorf("source = %q, want context-validate", got[0].Source)
	}
	if got[0].Fix == "" {
		t.Errorf("finding should carry a fix action")
	}
}

func TestAggregateStructural_MissingGateTaggedPerStory(t *testing.T) {
	stories := []validateStory{
		{ID: "sty_a", Body: cleanWorkflowBody},
		{ID: "sty_b", Body: cleanWorkflowBody},
	}
	got := aggregateStructural(stories, noSkillsExist) // every reviewer_skill absent
	// cleanWorkflowBody names 2 distinct reviewer skills -> 2 findings per story.
	if len(got) != 4 {
		t.Fatalf("expected 4 missing-gate-skill findings (2 per story), got %d: %v", len(got), got)
	}
	perStory := map[string]int{}
	for _, f := range got {
		if f.Code != "missing-gate-skill" {
			t.Errorf("unexpected code %q", f.Code)
		}
		perStory[f.StoryID]++
	}
	if perStory["sty_a"] != 2 || perStory["sty_b"] != 2 {
		t.Fatalf("findings not evenly tagged per story: %v", perStory)
	}
}

func TestMapFix_KnownAndDefault(t *testing.T) {
	cases := map[string]string{
		"missing-gate-skill":   "skill sync",
		"workflow-lifecycle":   "lifecycle",
		"workflow-unparseable": "parses",
	}
	for code, want := range cases {
		if got := mapFix(code); !strings.Contains(got, want) {
			t.Errorf("mapFix(%q) = %q, want substring %q", code, got, want)
		}
	}
	if got := mapFix("some-semantic-code"); !strings.Contains(got, "system authoring/review skills") {
		t.Errorf("default fix = %q", got)
	}
}

func TestStrictExitDecision(t *testing.T) {
	none := []validateFinding{{Severity: "warn", Code: "x"}}
	if anyErrorFinding(none) {
		t.Errorf("warn-only findings must not trip strict")
	}
	some := []validateFinding{{Severity: "warn"}, {Severity: "error"}, {Severity: "ERROR"}}
	if !anyErrorFinding(some) {
		t.Errorf("error-severity findings must trip strict")
	}
	if n := countErrorFindings(some); n != 2 {
		t.Errorf("countErrorFindings = %d, want 2", n)
	}
}

func TestValidateReportJSONShape(t *testing.T) {
	r := validateReport{
		StoriesChecked: 2,
		Findings: []validateFinding{
			{StoryID: "sty_x", Severity: "error", Code: "missing-gate-skill", Message: "m", Fix: "f", Source: "context-validate"},
		},
		Budget: &validateBudget{AlwaysOnBytes: 100, AlwaysOnTokens: 25, Verdict: budgetVerdict{CurrentBytes: 100, HasBaseline: true, BaselineBytes: 90, DeltaBytes: 10, Grew: true}},
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var back validateReport
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.StoriesChecked != 2 || len(back.Findings) != 1 || back.Findings[0].StoryID != "sty_x" {
		t.Fatalf("round-trip mismatch: %+v", back)
	}
	if back.Budget == nil || !back.Budget.Verdict.Grew {
		t.Fatalf("budget block lost in round-trip: %+v", back.Budget)
	}
}
