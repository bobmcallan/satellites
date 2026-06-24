package workstate

import (
	"path/filepath"
	"testing"
	"time"
)

// TestActiveGate covers the in-flight-gate marker (sty_8eb57090): set → get
// (fresh), latest-gate-wins on re-set, clear → absent, and that a reader can
// judge staleness from StartedAt. The marker lets a separate process (the
// stopcheck hook / work status) tell "a gate is running" from "engaged idle".
func TestActiveGate(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)

	// Absent on a fresh store.
	if _, ok, err := s.GetActiveGate("sty_a"); err != nil || ok {
		t.Fatalf("fresh store: want absent, got ok=%v err=%v", ok, err)
	}

	// Set → present with the gate name and start time.
	if err := s.SetActiveGate("sty_a", "satellites-integration-review", now); err != nil {
		t.Fatal(err)
	}
	ag, ok, err := s.GetActiveGate("sty_a")
	if err != nil || !ok {
		t.Fatalf("after set: want present, got ok=%v err=%v", ok, err)
	}
	if ag.Gate != "satellites-integration-review" || !ag.StartedAt.Equal(now) {
		t.Fatalf("roundtrip mismatch: %+v", ag)
	}

	// Re-set → latest gate wins (one row per story).
	later := now.Add(2 * time.Minute)
	if err := s.SetActiveGate("sty_a", "satellites-commit-push-review", later); err != nil {
		t.Fatal(err)
	}
	ag, _, _ = s.GetActiveGate("sty_a")
	if ag.Gate != "satellites-commit-push-review" || !ag.StartedAt.Equal(later) {
		t.Fatalf("latest gate must win, got %+v", ag)
	}

	// A different story is unaffected.
	if _, ok, _ := s.GetActiveGate("sty_b"); ok {
		t.Fatal("sty_b must have no marker")
	}

	// Clear → absent; clearing again is a no-op.
	if err := s.ClearActiveGate("sty_a"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.GetActiveGate("sty_a"); ok {
		t.Fatal("after clear: want absent")
	}
	if err := s.ClearActiveGate("sty_a"); err != nil {
		t.Fatalf("idempotent clear must not error: %v", err)
	}
}
