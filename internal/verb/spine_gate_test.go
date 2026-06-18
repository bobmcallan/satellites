package verb

import (
	"strings"
	"testing"
)

// TestIntentPlanReviewReducedToFormatGoal pins epic:minimal-spine order-5: the
// embedded spine plan gate judges story FORMAT + a story→done GOAL, and does NOT
// judge config-over-code (that is satellites-intent-code-review, on the diff).
// It is load-bearing on the reduction — if the gate ever reverts to deciding
// config-over-code, this test fails.
func TestIntentPlanReviewReducedToFormatGoal(t *testing.T) {
	raw, ok := internalGateRaw("satellites-intent-plan-review")
	if !ok {
		t.Fatal("satellites-intent-plan-review must remain embedded (the spine plan gate)")
	}
	body := string(raw)

	// Positive: the gate's single decision is story format + a done-able goal.
	for _, want := range []string{
		"is this story a well-formed, drivable contract",
		"satellites-formatted",
		"story→done",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("reduced spine gate must judge format+goal — missing %q", want)
		}
	}

	// Negative: the gate must NOT carry the old config-over-code decision — that
	// concern moved to satellites-intent-code-review (on the diff). The old gate
	// opened with this exact sentence; its return is the regression we forbid.
	if strings.Contains(body, "does this story's plan respect config-over-code") {
		t.Error("spine gate must NOT decide config-over-code — that is satellites-intent-code-review, on the diff")
	}
}
