package cli

import "testing"

// TestCheckGateHomeFork pins the home-of-gate control (epic:minimal-spine
// order-8): a materialised gate whose name is ALSO an embedded internal gate is
// a divergent shadow and must be reported as a blocking gate-home-fork; a
// materialised-only gate must not. This is the dogfood — a deliberately shadowed
// embedded gate fails the check.
func TestCheckGateHomeFork(t *testing.T) {
	// satellites-intent-plan-review is an embedded spine gate; materialising it
	// too is a fork.
	forked := []matSkill{{name: "satellites-intent-plan-review", kind: "gate"}}
	got := checkGateHomeFork(forked)
	if len(got) != 1 || got[0].Severity != "block" || got[0].Code != "gate-home-fork" {
		t.Fatalf("a gate that is both embedded and materialised must yield one gate-home-fork block, got %+v", got)
	}
	if got[0].Artifact != "satellites-intent-plan-review" {
		t.Fatalf("finding must name the forked gate, got %q", got[0].Artifact)
	}

	// Materialised-only gates (not embedded) have a single home — no fork.
	clean := []matSkill{
		{name: "satellites-story-plan-review", kind: "gate"},
		{name: "satellites-technical-debt-review", kind: "gate"},
		{name: "satellites-intent-code-review", kind: "gate"}, // moved to substrate in order-3
	}
	if got := checkGateHomeFork(clean); len(got) != 0 {
		t.Fatalf("materialised-only gates must not fork, got %+v", got)
	}
}
