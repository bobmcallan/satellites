package cli

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/workstate"
)

// TestTickEngagement pins the ticker's throttle semantics (sty_84c55d0d):
// a fresh-leased engagement older than the throttle window is re-stamped, a
// recently-stamped one is not, and candidates / expired leases never tick.
func TestTickEngagement(t *testing.T) {
	db := filepath.Join(t.TempDir(), "state.db")
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)

	store, err := workstate.Open(db)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := store.Append(workstate.Event{
		Session: "s1", Story: "sty_t", Phase: "in_progress", Kind: "engage",
		LeaseUntil: now.Add(2 * time.Hour), Editable: true, TS: now,
	}); err != nil {
		t.Fatalf("seed engage: %v", err)
	}
	store.Close()

	t.Run("fresh stamp inside throttle does not tick", func(t *testing.T) {
		ticked, err := tickEngagement(db, "s1", now.Add(10*time.Second))
		if err != nil {
			t.Fatalf("tick: %v", err)
		}
		if ticked {
			t.Error("tick inside the throttle window must be skipped")
		}
	})

	t.Run("stamp older than throttle ticks and refreshes the lease", func(t *testing.T) {
		at := now.Add(engagementTickThrottle + time.Second)
		ticked, err := tickEngagement(db, "s1", at)
		if err != nil {
			t.Fatalf("tick: %v", err)
		}
		if !ticked {
			t.Fatal("stale stamp must tick")
		}
		s, _ := workstate.Open(db)
		defer s.Close()
		eng, ok, err := s.Current("s1", "sty_t")
		if err != nil || !ok {
			t.Fatalf("current: %v ok=%v", err, ok)
		}
		if !eng.LeaseUntil.Equal(at.Add(engageLeaseTTL)) {
			t.Errorf("lease not refreshed: %v", eng.LeaseUntil)
		}
		if eng.Phase != "in_progress" {
			t.Errorf("phase must carry over, got %q", eng.Phase)
		}
	})

	t.Run("expired lease does not tick", func(t *testing.T) {
		ticked, err := tickEngagement(db, "s1", now.Add(5*time.Hour))
		if err != nil {
			t.Fatalf("tick: %v", err)
		}
		if ticked {
			t.Error("an expired lease is not processing — must not tick")
		}
	})

	t.Run("candidate rows do not tick", func(t *testing.T) {
		s, _ := workstate.Open(db)
		if _, err := s.Append(workstate.Event{
			Session: "s2", Story: "sty_c", Phase: phaseCandidate, Kind: "candidate",
			LeaseUntil: now.Add(2 * time.Hour), TS: now,
		}); err != nil {
			t.Fatalf("seed candidate: %v", err)
		}
		s.Close()
		ticked, err := tickEngagement(db, "s2", now.Add(time.Minute))
		if err != nil {
			t.Fatalf("tick: %v", err)
		}
		if ticked {
			t.Error("a candidate (read-only access) must not tick")
		}
	})
}
