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
	"github.com/bobmcallan/satellites/internal/blob"
	"github.com/bobmcallan/satellites/internal/db"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ingest"
	"github.com/bobmcallan/satellites/internal/invitation"
	"github.com/bobmcallan/satellites/internal/server"
	"github.com/bobmcallan/satellites/internal/workspace"
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
	// stories were unified into documents in migration 0017; type='story'
	// rows live in documents so the single TRUNCATE there covers both.
	if _, err := env.DB.Exec(`
        TRUNCATE tools, reviews, evidence,
                 documents, document_versions, variables,
                 system_seeds
        RESTART IDENTITY CASCADE
    `); err != nil {
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

	// Mirror the production post-auth provisioning (personal workspace + invite
	// claim) so login paths behave as they do on the server (sty_480dba9b).
	wsStore := workspace.New(base.DB)
	invStore := invitation.New(base.DB)
	// Mirror the production binary-ingestion wiring (cmd/satellites-server) so the
	// blob/document upload routes register and behave identically in tests.
	blobStore := blob.New(base.DB)
	docStore := document.New(base.DB)
	handler := server.Build(server.Config{
		Store:   store,
		DevMode: true,
		ProvisionLogin: func(ctx context.Context, userID, email, displayName string) error {
			if _, err := wsStore.EnsurePersonalWorkspace(ctx, userID, displayName, time.Now().UTC()); err != nil {
				return err
			}
			_, err := invStore.ClaimForEmail(ctx, email, userID, time.Now().UTC())
			return err
		},
		StoreBlob: func(ctx context.Context, up server.BlobUpload) (server.BlobRef, error) {
			ref, err := ingest.StoreBlobAndExtract(ctx, blobStore, docStore, ingest.Upload{
				WorkspaceID: up.WorkspaceID,
				ProjectID:   up.ProjectID,
				Filename:    up.Filename,
				ContentType: up.ContentType,
				CreatedBy:   up.CreatedBy,
				Content:     up.Content,
			})
			if err != nil {
				return server.BlobRef{}, err
			}
			return server.BlobRef{
				ID:          ref.ID,
				ProjectID:   ref.ProjectID,
				Filename:    ref.Filename,
				ContentType: ref.ContentType,
				SizeBytes:   ref.SizeBytes,
				SHA256:      ref.SHA256,
				DocumentID:  ref.DocumentID,
				Extracted:   ref.Extracted,
			}, nil
		},
		GetBlob: func(ctx context.Context, blobID string) (server.BlobContent, error) {
			b, content, err := blobStore.GetContent(ctx, blobID)
			if err != nil {
				return server.BlobContent{}, err
			}
			return server.BlobContent{
				WorkspaceID: b.WorkspaceID,
				ProjectID:   b.ProjectID,
				Filename:    b.Filename,
				ContentType: b.ContentType,
				Content:     content,
			}, nil
		},
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return &ServerEnv{
		Env:       base,
		Store:     store,
		ServerURL: srv.URL,
	}
}
