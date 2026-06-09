package server

import (
	"testing"
	"time"
)

func TestComputeActivity(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	mk := func(kind string, ago time.Duration, sess string) activityEntry {
		return activityEntry{Kind: kind, CreatedAt: now.Add(-ago), SessionID: sess}
	}
	tests := []struct {
		name        string
		entries     []activityEntry
		wantActive  bool
		wantSession string
	}{
		{"recent engage is active", []activityEntry{mk("engagement:engage", 30*time.Second, "s1")}, true, "s1"},
		{"picks the latest engagement by time", []activityEntry{
			mk("engagement:engage", 5*time.Minute, "s1"),
			mk("engagement:touch", 20*time.Second, "s2"),
		}, true, "s2"},
		{"stale engagement is inactive", []activityEntry{mk("engagement:engage", 10*time.Minute, "s1")}, false, ""},
		{"latest event a close is inactive", []activityEntry{
			mk("engagement:touch", 2*time.Minute, "s1"),
			mk("engagement:close", 30*time.Second, "s1"),
		}, false, ""},
		{"non-engagement rows ignored", []activityEntry{mk("status_transition", 10*time.Second, "")}, false, ""},
		{"empty is inactive", nil, false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := computeActivity(tc.entries, now)
			if got.Active != tc.wantActive {
				t.Fatalf("Active=%v, want %v", got.Active, tc.wantActive)
			}
			if tc.wantActive && got.Session != tc.wantSession {
				t.Fatalf("Session=%q, want %q", got.Session, tc.wantSession)
			}
			if tc.wantActive && got.Since == "" {
				t.Fatalf("active result missing Since")
			}
		})
	}
}
