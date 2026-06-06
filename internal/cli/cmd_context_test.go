package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEstTokens(t *testing.T) {
	cases := map[int]int{0: 0, 1: 1, 4: 1, 5: 2, 8: 2, 1754: 439}
	for in, want := range cases {
		if got := estTokens(in); got != want {
			t.Errorf("estTokens(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestExtractSection(t *testing.T) {
	body := "# Title\n\n## Plan\nstep one\nstep two\n\n## Workflow\n```yaml\nstates: []\n```\n\n## Context\ntail\n"
	wf := extractSection(body, "## Workflow")
	if wf == "" {
		t.Fatal("expected a ## Workflow section, got empty")
	}
	if want := "## Workflow"; wf[:len(want)] != want {
		t.Errorf("section should start at the heading, got %q", wf)
	}
	if got := extractSection(wf, "states:"); got != "" {
		t.Errorf("expected empty for a heading not present, got %q", got)
	}
	// stops before the next ## heading
	if containsSubstr(wf, "## Context") || containsSubstr(wf, "tail") {
		t.Errorf("section bled past the next heading: %q", wf)
	}
	if got := extractSection(body, "## Missing"); got != "" {
		t.Errorf("missing heading should yield empty, got %q", got)
	}
}

func TestReviewerSkillsFromWorkflow(t *testing.T) {
	wf := "## Workflow\n```yaml\ntransitions:\n  - {from: backlog,     to: in_progress, reviewer_skill: \"satellites-story-plan-review\"}\n  - {from: in_progress, to: done,        reviewer_skill: \"satellites-story-done-review\"}\n```\n"
	got := reviewerSkillsFromWorkflow(wf)
	want := []string{"satellites-story-done-review", "satellites-story-plan-review"} // sorted
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	// duplicate reviewer_skill across edges collapses to one
	dup := "reviewer_skill: \"x\"\nreviewer_skill: \"x\"\n"
	if g := reviewerSkillsFromWorkflow(dup); len(g) != 1 || g[0] != "x" {
		t.Errorf("dedup failed: %v", g)
	}
	if g := reviewerSkillsFromWorkflow("no skills here"); len(g) != 0 {
		t.Errorf("expected none, got %v", g)
	}
}

func TestSumPartitionTotals(t *testing.T) {
	comps := []contextComponent{
		{Name: "load-context", Partition: partAlwaysOn, Bytes: 1000},
		{Name: "skills", Partition: partAlwaysOn, Bytes: 500},
		{Name: "envelope", Partition: partPerGate, Bytes: 4000},
		{Name: "workflow", Partition: partWithinEnvelope, Bytes: 300}, // excluded
	}
	tot := sumPartitionTotals(comps)
	if tot[partAlwaysOn] != 1500 {
		t.Errorf("always-on = %d, want 1500", tot[partAlwaysOn])
	}
	if tot[partPerGate] != 4000 {
		t.Errorf("per-gate = %d, want 4000", tot[partPerGate])
	}
	if _, ok := tot[partWithinEnvelope]; ok {
		t.Errorf("within-envelope must be excluded from totals, got %d", tot[partWithinEnvelope])
	}
	if tot["grand"] != 5500 {
		t.Errorf("grand = %d, want 5500 (always-on + per-gate, no within-envelope)", tot["grand"])
	}
}

func TestSkillsIndexSize(t *testing.T) {
	dir := t.TempDir()
	skills := filepath.Join(dir, ".claude", "skills")
	mk := func(name, content string) {
		d := filepath.Join(skills, name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// one plain skill, one carrying a sync-stamp ahead of frontmatter
	mk("alpha", "---\nname: alpha\ndescription: does alpha\n---\nbody\n")
	mk("beta", "<!-- satellites-sync:begin {\"x\":1} satellites-sync:end -->\n---\nname: beta\ndescription: does beta things\n---\nbody\n")
	t.Chdir(dir)

	bytes, count, err := skillsIndexSize()
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2 (sync-stamped skill must parse)", count)
	}
	// len(name)+len(desc)+2 per skill: alpha(5+10+2=17) + beta(4+16+2=22) = 39
	if want := 39; bytes != want {
		t.Errorf("bytes = %d, want %d", bytes, want)
	}
}

func TestDeliveredContextJSONRoundTrip(t *testing.T) {
	dc := deliveredContext{
		StoryID: "sty_test",
		Components: []contextComponent{
			{Name: "load-context", Source: "embed", Partition: partAlwaysOn, Bytes: 1754, Tokens: estTokens(1754)},
		},
	}
	dc.TotalsBytes = sumPartitionTotals(dc.Components)
	raw, err := json.Marshal(dc)
	if err != nil {
		t.Fatal(err)
	}
	var back deliveredContext
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.StoryID != "sty_test" || len(back.Components) != 1 {
		t.Fatalf("round-trip mismatch: %+v", back)
	}
	if back.TotalsBytes["grand"] != 1754 {
		t.Errorf("grand = %d, want 1754", back.TotalsBytes["grand"])
	}
}

func containsSubstr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
