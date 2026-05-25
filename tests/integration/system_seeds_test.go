//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestSystemSeedReconcile covers the three boot scenarios called out in
// sty_c19e64bf AC#5: fresh DB (insert), no-change boot (zero writes),
// drift detected (replace + bump applied_at + new document version).
func TestSystemSeedReconcile(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	docs := document.New(env.DB)
	sys := document.NewSystemSeedStore(env.DB)
	ctx := context.Background()
	name := "principles_test"
	body := "principle 1: prefer the smallest change."

	t.Run("fresh DB inserts both rows", func(t *testing.T) {
		now := time.Date(2026, 5, 25, 10, 0, 0, 0, time.UTC)
		res, err := document.ReconcileSystemSeed(ctx, sys, docs, name, body, "system:seed", now)
		if err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		if !res.Created || !res.Changed {
			t.Fatalf("expected Created=true Changed=true, got %+v", res)
		}
		got, err := sys.Get(ctx, name)
		if err != nil {
			t.Fatalf("get system_seed: %v", err)
		}
		if got.Body != body || got.EmbeddedHash != document.HashBody(body) {
			t.Fatalf("system_seeds row mismatch: %+v", got)
		}
		mirror, err := docs.Get(ctx, document.Key{Scope: document.ScopeSystem, Name: name}, document.GetOptions{})
		if err != nil {
			t.Fatalf("mirrored document missing: %v", err)
		}
		if len(mirror.Versions) != 1 || mirror.Versions[0].Body != body {
			t.Fatalf("mirrored document body mismatch: %+v", mirror.Versions)
		}
	})

	t.Run("no-change boot writes nothing", func(t *testing.T) {
		before, err := sys.Get(ctx, name)
		if err != nil {
			t.Fatalf("get before: %v", err)
		}
		later := before.AppliedAt.Add(1 * time.Hour)
		res, err := document.ReconcileSystemSeed(ctx, sys, docs, name, body, "system:seed", later)
		if err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		if res.Changed || res.Created {
			t.Fatalf("expected Changed=false Created=false, got %+v", res)
		}
		after, err := sys.Get(ctx, name)
		if err != nil {
			t.Fatalf("get after: %v", err)
		}
		if !after.AppliedAt.Equal(before.AppliedAt) {
			t.Fatalf("applied_at advanced on no-op: before=%v after=%v", before.AppliedAt, after.AppliedAt)
		}
		mirror, err := docs.Get(ctx, document.Key{Scope: document.ScopeSystem, Name: name}, document.GetOptions{AllVersions: true})
		if err != nil {
			t.Fatalf("get mirror: %v", err)
		}
		if len(mirror.Versions) != 1 {
			t.Fatalf("expected one document version after no-op, got %d", len(mirror.Versions))
		}
	})

	t.Run("drift triggers replace + new document version", func(t *testing.T) {
		// Simulate drift: rewrite system_seeds.embedded_hash to a stale
		// value so the next reconcile sees the embed body as different.
		if _, err := env.DB.Exec(`UPDATE system_seeds SET embedded_hash = 'stale' WHERE name = $1`, name); err != nil {
			t.Fatalf("inject drift: %v", err)
		}
		before, err := sys.Get(ctx, name)
		if err != nil {
			t.Fatalf("get before drift reconcile: %v", err)
		}
		// Force a different applied_at by waiting at least one microsecond;
		// use a value 1s past the previous to make the assertion robust
		// against same-instant clock reads.
		later := before.AppliedAt.Add(1 * time.Second)
		res, err := document.ReconcileSystemSeed(ctx, sys, docs, name, body, "system:seed", later)
		if err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		if !res.Changed || res.Created {
			t.Fatalf("expected Changed=true Created=false, got %+v", res)
		}
		after, err := sys.Get(ctx, name)
		if err != nil {
			t.Fatalf("get after: %v", err)
		}
		if after.EmbeddedHash != document.HashBody(body) {
			t.Fatalf("hash not restored: %q", after.EmbeddedHash)
		}
		if !after.AppliedAt.After(before.AppliedAt) {
			t.Fatalf("applied_at not advanced: before=%v after=%v", before.AppliedAt, after.AppliedAt)
		}
		mirror, err := docs.Get(ctx, document.Key{Scope: document.ScopeSystem, Name: name}, document.GetOptions{AllVersions: true})
		if err != nil {
			t.Fatalf("get mirror: %v", err)
		}
		// Mirror gets a new version only when the body actually changed
		// vs the latest active version. Drift in system_seeds.hash with
		// the same body MUST NOT spuriously bump documents (SeedSystem
		// is body-byte idempotent). So expect exactly one version here.
		if len(mirror.Versions) != 1 {
			t.Fatalf("expected one document version (body unchanged), got %d", len(mirror.Versions))
		}
	})
}

// TestSystemSeedStore_GetMissing confirms ErrNotFound semantics — the
// Reconcile path depends on this sentinel.
func TestSystemSeedStore_GetMissing(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)
	sys := document.NewSystemSeedStore(env.DB)
	_, err := sys.Get(context.Background(), "absent")
	if !errors.Is(err, document.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
