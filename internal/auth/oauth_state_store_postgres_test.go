//go:build integration

package auth_test

import (
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestPGStateStore_SurvivesProcessBoundary is the regression test for
// the Fly "invalid state" outage: two PGStateStore instances bound to
// the same DB act like two satellites-server processes. Minting on
// store A and consuming on store B must succeed.
//
// The companion sub-test asserts that MemStateStore *cannot* round-trip
// across instances — pinning the failure mode the old prod wiring had.
func TestPGStateStore_SurvivesProcessBoundary(t *testing.T) {
	env := testbootstrap.SetUp(t)

	t.Run("pg_states_survive", func(t *testing.T) {
		instanceA := auth.NewPGStateStore(env.DB, time.Minute)
		instanceB := auth.NewPGStateStore(env.DB, time.Minute)

		id, err := instanceA.Mint()
		if err != nil {
			t.Fatalf("mint on instance A: %v", err)
		}
		if err := instanceB.Consume(id); err != nil {
			t.Fatalf("consume on instance B: %v — state did not survive process boundary", err)
		}
		// Replay must still fail on either instance.
		if err := instanceA.Consume(id); err == nil {
			t.Fatal("replay on A should fail")
		}
		if err := instanceB.Consume(id); err == nil {
			t.Fatal("replay on B should fail")
		}
	})

	t.Run("mem_states_do_not_survive_pins_failure_mode", func(t *testing.T) {
		instanceA := auth.NewStateStore(time.Minute)
		instanceB := auth.NewStateStore(time.Minute)

		id, err := instanceA.Mint()
		if err != nil {
			t.Fatal(err)
		}
		// This MUST fail — that's the bug. If it ever starts passing,
		// someone has accidentally made MemStateStore durable and the
		// regression net needs revisiting.
		if err := instanceB.Consume(id); err == nil {
			t.Fatal("MemStateStore unexpectedly shared state across instances — failure mode no longer pinned")
		}
	})
}

// TestPGStateStore_Expired verifies a state past expires_at is rejected
// with "expired state" (not "unknown state") so operators can tell the
// two failure modes apart in logs.
func TestPGStateStore_Expired(t *testing.T) {
	env := testbootstrap.SetUp(t)
	s := auth.NewPGStateStore(env.DB, time.Millisecond)

	id, err := s.Mint()
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	err = s.Consume(id)
	if err == nil {
		t.Fatal("expected error for expired state")
	}
	if got := err.Error(); got != "oauth: expired state" {
		t.Errorf("error: got %q want %q", got, "oauth: expired state")
	}
}

// TestPGStateStore_EmptyOrUnknown matches MemStateStore semantics so
// the two implementations are interchangeable from a caller's POV.
func TestPGStateStore_EmptyOrUnknown(t *testing.T) {
	env := testbootstrap.SetUp(t)
	s := auth.NewPGStateStore(env.DB, time.Minute)

	if err := s.Consume(""); err == nil {
		t.Error("empty state should fail")
	}
	if err := s.Consume("never-minted"); err == nil {
		t.Error("unknown state should fail")
	}
}
