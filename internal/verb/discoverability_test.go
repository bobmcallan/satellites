package verb

import (
	"strings"
	"testing"
)

// TestGoverningEdgesHint pins the next-gate naming behind sty_4300e117: from a
// state, the hint names every available transition WITH the --skill <gate> that
// drives it, so a stuck agent is pointed at the real gate instead of guessing or
// hand-stamping. Empty status set returns "".
func TestGoverningEdgesHint(t *testing.T) {
	// skills-wf: ready --[start]--> in_progress.
	hint := GoverningEdgesHint("", skillsWFBody, "ready", "skill", sources())
	if !strings.Contains(hint, "in_progress") || !strings.Contains(hint, "--skill start") {
		t.Fatalf("hint from ready = %q, want it to name → in_progress (--skill start)", hint)
	}
	// A terminal state with no outgoing edge yields no hint.
	if h := GoverningEdgesHint("", skillsWFBody, "done", "skill", sources()); h != "" {
		t.Fatalf("hint from terminal done = %q, want empty", h)
	}
}

// TestConfigSkillBody pins the skill-get discoverability fix: a binary-embedded
// system reviewer resolves (so `skill get` surfaces it instead of "not found"),
// while an unknown name does not.
func TestConfigSkillBody(t *testing.T) {
	if body, ok := ConfigSkillBody("satellites-task-report-review"); !ok || strings.TrimSpace(body) == "" {
		t.Fatalf("ConfigSkillBody(report-review): ok=%v len=%d, want a non-empty embedded body", ok, len(body))
	}
	if _, ok := ConfigSkillBody("definitely-not-an-embedded-reviewer"); ok {
		t.Fatalf("ConfigSkillBody(bogus): ok=true, want false")
	}
	// And the list surfaces the embedded task gates.
	names := ConfigSkillNames()
	found := false
	for _, n := range names {
		if n == "satellites-task-report-review" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ConfigSkillNames did not include satellites-task-report-review; got %v", names)
	}
}
