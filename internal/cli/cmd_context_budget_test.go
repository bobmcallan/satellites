package cli

import "testing"

// TestEvalBudget_RatchetDirection pins AC6: growth past baseline flags; a shrink
// or an exact match does not; with no baseline nothing flags.
func TestEvalBudget_RatchetDirection(t *testing.T) {
	cases := []struct {
		name              string
		current, baseline int
		hasBaseline       bool
		wantGrew          bool
		wantDelta         int
	}{
		{"growth flags", 9000, 8700, true, true, 300},
		{"shrink does not flag", 8000, 8700, true, false, -700},
		{"equal does not flag", 8700, 8700, true, false, 0},
		{"no baseline never flags", 9999, 0, false, false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := evalBudget(c.current, c.baseline, c.hasBaseline)
			if v.Grew != c.wantGrew {
				t.Errorf("Grew = %v, want %v", v.Grew, c.wantGrew)
			}
			if c.hasBaseline && v.DeltaBytes != c.wantDelta {
				t.Errorf("DeltaBytes = %d, want %d", v.DeltaBytes, c.wantDelta)
			}
			if v.HasBaseline != c.hasBaseline {
				t.Errorf("HasBaseline = %v, want %v", v.HasBaseline, c.hasBaseline)
			}
		})
	}
}

// TestAlwaysOnSize_StablePerComponent pins AC1/AC6: alwaysOnSize extracts only
// the always-on partition, sums it as the tracked number, and orders components
// by size — stable numbers over a known fixture (per-gate rows excluded).
func TestAlwaysOnSize_StablePerComponent(t *testing.T) {
	dc := deliveredContext{
		Components: []contextComponent{
			{Name: "skills index", Partition: partAlwaysOn, Bytes: 5564, Tokens: estTokens(5564)},
			{Name: "load-context", Partition: partAlwaysOn, Bytes: 1754, Tokens: estTokens(1754)},
			{Name: "story envelope", Partition: partPerGate, Bytes: 5200, Tokens: estTokens(5200)},
			{Name: "## Workflow", Partition: partWithinEnvelope, Bytes: 800},
		},
		TotalsBytes: map[string]int{partAlwaysOn: 7318, partPerGate: 5200},
	}
	bytes, comps := alwaysOnSize(dc)
	if bytes != 7318 {
		t.Fatalf("always-on bytes = %d, want 7318 (per-gate/within-envelope excluded)", bytes)
	}
	if len(comps) != 2 {
		t.Fatalf("always-on components = %d, want 2", len(comps))
	}
	// Ordered by size descending (skills index dominates — the spike's finding).
	if comps[0].Name != "skills index" || comps[1].Name != "load-context" {
		t.Fatalf("components not size-ordered: %+v", comps)
	}
}
