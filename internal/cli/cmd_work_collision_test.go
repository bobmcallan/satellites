package cli

import (
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/workstate"
)

// TestFindEpicCollisions pins the predicate (sty_6a045c2c): only a DIFFERENT
// session, a DIFFERENT story, a FRESH lease, and the SAME non-empty epic counts.
func TestFindEpicCollisions(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(time.Hour)
	stale := now.Add(-time.Hour)

	parent := map[string]string{
		"sty_a": "epic_1", // mine
		"sty_b": "epic_1", // sibling under my epic
		"sty_c": "epic_2", // different epic
		"sty_d": "epic_1", // sibling, but only held by my own session / a stale lease
	}
	parentOf := func(s string) string { return parent[s] }

	engs := []workstate.Engagement{
		{Session: "sess_other", Story: "sty_b", LeaseUntil: fresh}, // ← the one collision
		{Session: "sess_other", Story: "sty_c", LeaseUntil: fresh}, // different epic
		{Session: "sess_mine", Story: "sty_d", LeaseUntil: fresh},  // my own other engagement
		{Session: "sess_x", Story: "sty_d", LeaseUntil: stale},     // stale lease
		{Session: "sess_mine", Story: "sty_a", LeaseUntil: fresh},  // myself
	}

	got := findEpicCollisions("sess_mine", "sty_a", "epic_1", engs, parentOf, now)
	if len(got) != 1 || got[0].Story != "sty_b" || got[0].Session != "sess_other" {
		t.Fatalf("want one collision (sty_b/sess_other), got %+v", got)
	}

	// Empty epic ⇒ nothing to collide on.
	if c := findEpicCollisions("sess_mine", "sty_a", "", engs, parentOf, now); len(c) != 0 {
		t.Errorf("empty parent must yield no collisions, got %d", len(c))
	}
}
