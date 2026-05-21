//go:build integration

package integration_test

import (
	"context"
	"testing"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestSessionSecret_PersistsAcrossLoad confirms the secret is durable
// in the DB: two LoadOrCreate calls on the same database return the
// same secret. This is the property that keeps users logged in across
// container restarts.
func TestSessionSecret_PersistsAcrossLoad(t *testing.T) {
	env := testbootstrap.SetUp(t)
	ctx := context.Background()

	first, err := auth.LoadOrCreateSessionSecret(ctx, env.DB)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	if len(first) != 32 {
		t.Fatalf("secret length: got %d want 32", len(first))
	}

	// Subsequent boots see the same secret — no fresh mint.
	second, err := auth.LoadOrCreateSessionSecret(ctx, env.DB)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("secret drifted across loads — sessions would log out on every restart")
	}

	// And again, just to confirm there's no per-call mint.
	third, err := auth.LoadOrCreateSessionSecret(ctx, env.DB)
	if err != nil {
		t.Fatalf("third load: %v", err)
	}
	if string(first) != string(third) {
		t.Fatalf("secret drifted on third load")
	}
}

// TestSessionSecret_PersistsAcrossDevSeed exercises the same path the
// dev stack runs: migrations applied, DevSeed inserts users + keys,
// LoadOrCreateSessionSecret runs. Re-running DevSeed (idempotent boot)
// does not change the session secret — cookies issued before the
// restart still verify.
func TestSessionSecret_PersistsAcrossDevSeed(t *testing.T) {
	env := testbootstrap.SetUp(t)
	store := auth.New(env.DB)
	ctx := context.Background()

	if err := store.DevSeed(ctx); err != nil {
		t.Fatalf("first DevSeed: %v", err)
	}
	first, err := auth.LoadOrCreateSessionSecret(ctx, env.DB)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}

	if err := store.DevSeed(ctx); err != nil {
		t.Fatalf("second DevSeed (simulating restart): %v", err)
	}
	second, err := auth.LoadOrCreateSessionSecret(ctx, env.DB)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}

	if string(first) != string(second) {
		t.Fatal("session secret changed across simulated restart — users would be logged out")
	}
}
