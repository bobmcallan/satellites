package server

import (
	"encoding/json"
	"testing"
	"time"
)

// TestFoldEngagement pins the per-(session, story) fold (sty_84c55d0d):
// newest row wins, payload phase/lease are preferred, and rows missing a
// correlation id are dropped.
func TestFoldEngagement(t *testing.T) {
	byPair := map[string]engagementView{}
	t0 := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)

	foldEngagement(byPair, "s1", "sty_a", "in_progress", json.RawMessage(`{"phase":"in_progress","seq":1,"lease_until":"2026-06-12T12:00:00Z"}`), t0)
	foldEngagement(byPair, "s1", "sty_a", "done-review", json.RawMessage(`{"phase":"done-review","seq":2,"lease_until":"2026-06-12T12:30:00Z"}`), t0.Add(time.Minute))
	foldEngagement(byPair, "s2", "sty_b", "body-phase", nil, t0)
	foldEngagement(byPair, "", "sty_c", "x", nil, t0)
	foldEngagement(byPair, "s3", "", "x", nil, t0)

	if len(byPair) != 2 {
		t.Fatalf("pairs = %d, want 2 (id-less rows dropped): %v", len(byPair), byPair)
	}
	a := byPair["s1|sty_a"]
	if a.Phase != "done-review" || !a.LastSeen.Equal(t0.Add(time.Minute)) || a.LeaseUntil != "2026-06-12T12:30:00Z" {
		t.Errorf("newest row must win with payload fields: %+v", a)
	}
	if b := byPair["s2|sty_b"]; b.Phase != "body-phase" {
		t.Errorf("missing payload phase must fall back to body: %+v", b)
	}
}
