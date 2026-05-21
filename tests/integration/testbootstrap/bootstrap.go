//go:build integration

// Package testbootstrap is the shared setup/teardown helper for V5's
// integration tests. One bootstrap function across all sections; each
// test calls Reset to enforce clean data injection between scenarios.
//
// Two flavours:
//   - SetUp(t)           — Postgres only (used by DB-shape and store tests)
//   - SetUpWithServer(t) — Postgres + auth.Store + DevSeed + satellites-server
//     via httptest. This is the canonical bootstrap
//     for browser-driven (chromedp) integration tests.
package testbootstrap

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/db"
	"github.com/bobmcallan/satellites/internal/server"
	_ "github.com/lib/pq"
	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Env is the per-test runtime: a live Postgres container with the V5
// schema applied. Each call to SetUp returns a fresh Env; the container
// is terminated automatically on test exit (success or failure).
type Env struct {
	DSN string
	DB  *sql.DB
}

// ServerEnv extends Env with a live satellites-server (httptest) bound
// to the test Postgres + a seeded dev-mode auth store.
type ServerEnv struct {
	*Env
	Store     *auth.Store
	ServerURL string
}

// SetUp boots a Postgres container, applies migrations, opens a
// database/sql handle, and registers teardown via t.Cleanup. Used from
// every integration test — the single source of truth for setup.
func SetUp(t *testing.T) *Env {
	t.Helper()
	ctx := context.Background()

	container, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("satellites"),
		postgres.WithUsername("satellites"),
		postgres.WithPassword("satellites"),
		tc.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(ctx); err != nil {
			t.Logf("terminate postgres container: %v", err)
		}
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	if err := db.MigrateUp(dsn); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	sqlDB, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	return &Env{DSN: dsn, DB: sqlDB}
}

// Reset truncates every V5 schema table, restoring the DB to its
// post-migration empty state. Call between subtests / sections for
// clean data injection.
//
// evidence carries an append-only trigger on UPDATE/DELETE; TRUNCATE
// bypasses row-level triggers, so it's the supported reset path.
func Reset(t *testing.T, env *Env) {
	t.Helper()
	if _, err := env.DB.Exec(`TRUNCATE stories, tools, reviews, evidence RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("reset tables: %v", err)
	}
}

// SetUpWithServer is the canonical browser-driven integration bootstrap:
// Postgres + migrations + auth.Store + DevSeed + the full
// satellites-server handler stack behind httptest.NewServer.
//
// chromedp tests Navigate to env.ServerURL/...; the dev-mode admin/user
// accounts are seeded so auth-gated paths work with sk_dev_admin /
// sk_dev_user.
func SetUpWithServer(t *testing.T) *ServerEnv {
	t.Helper()
	base := SetUp(t)

	store := auth.New(base.DB)
	if err := store.DevSeed(context.Background()); err != nil {
		t.Fatalf("dev seed: %v", err)
	}

	handler := server.Build(server.Config{
		Store:   store,
		DevMode: true,
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return &ServerEnv{
		Env:       base,
		Store:     store,
		ServerURL: srv.URL,
	}
}
