package server

import (
	"encoding/json"
	"testing"
	"time"
)

// TestFoldEngagement pins the QUALIFIED per-(session, story) fold
// (sty_84c55d0d shape, sty_07bb85b6 semantics): only working kinds
// (engage/tick/phase) light a pair, a candidate (read access) never does, a
// close clears the pair, newest working row wins, payload phase/lease are
// preferred, and rows missing a correlation id are dropped.
func TestFoldEngagement(t *testing.T) {
	byPair := map[string]engagementView{}
	t0 := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)

	foldEngagement(byPair, "engagement:engage", "s1", "sty_a", "in_progress", json.RawMessage(`{"phase":"in_progress","seq":1,"lease_until":"2026-06-12T12:00:00Z"}`), t0)
	foldEngagement(byPair, "engagement:tick", "s1", "sty_a", "done-review", json.RawMessage(`{"phase":"done-review","seq":2,"lease_until":"2026-06-12T12:30:00Z"}`), t0.Add(time.Minute))
	foldEngagement(byPair, "engagement:phase", "s2", "sty_b", "body-phase", nil, t0)
	foldEngagement(byPair, "engagement:tick", "", "sty_c", "x", nil, t0)
	foldEngagement(byPair, "engagement:tick", "s3", "", "x", nil, t0)

	if len(byPair) != 2 {
		t.Fatalf("pairs = %d, want 2 (id-less rows dropped): %v", len(byPair), byPair)
	}
	a := byPair["s1|sty_a"]
	if a.Phase != "done-review" || !a.LastSeen.Equal(t0.Add(time.Minute)) || a.LeaseUntil != "2026-06-12T12:30:00Z" {
		t.Errorf("newest working row must win with payload fields: %+v", a)
	}
	if b := byPair["s2|sty_b"]; b.Phase != "body-phase" {
		t.Errorf("missing payload phase must fall back to body: %+v", b)
	}

	t.Run("candidate never lights a pair", func(t *testing.T) {
		m := map[string]engagementView{}
		foldEngagement(m, "engagement:candidate", "s9", "sty_r", "candidate", nil, t0)
		if len(m) != 0 {
			t.Errorf("a read-access candidate must not light the indicator: %v", m)
		}
		// ...nor may a later candidate overwrite a working signal.
		foldEngagement(m, "engagement:tick", "s9", "sty_r", "in_progress", nil, t0)
		foldEngagement(m, "engagement:candidate", "s9", "sty_r", "candidate", nil, t0.Add(time.Minute))
		if v := m["s9|sty_r"]; !v.LastSeen.Equal(t0) {
			t.Errorf("candidate must not refresh a working pair: %+v", v)
		}
	})

	t.Run("close clears the pair", func(t *testing.T) {
		m := map[string]engagementView{}
		foldEngagement(m, "engagement:tick", "s9", "sty_r", "in_progress", nil, t0)
		foldEngagement(m, "engagement:close", "s9", "sty_r", "", nil, t0.Add(time.Minute))
		if len(m) != 0 {
			t.Errorf("a close must clear the pair: %v", m)
		}
	})

	t.Run("unknown kinds are ignored", func(t *testing.T) {
		m := map[string]engagementView{}
		foldEngagement(m, "engagement:mystery", "s9", "sty_r", "x", nil, t0)
		if len(m) != 0 {
			t.Errorf("unknown engagement kinds must not light the indicator: %v", m)
		}
	})
}

// TestEngagementVisibleStatus pins the status gate (sty_07bb85b6 AC2): not
// started and terminal rows never wear the indicator.
func TestEngagementVisibleStatus(t *testing.T) {
	for _, s := range []string{"backlog", "done", "cancelled", "canceled", "", "  Done "} {
		if engagementVisibleStatus(s) {
			t.Errorf("status %q must hide the indicator", s)
		}
	}
	for _, s := range []string{"ready", "in_progress", "techdebt-review", "integration-review", "done-review", "blocked"} {
		if !engagementVisibleStatus(s) {
			t.Errorf("status %q must allow the indicator", s)
		}
	}
}
