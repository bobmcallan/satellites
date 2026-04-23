package integration

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	satarbor "github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/db"
	"github.com/bobmcallan/satellites/internal/workspace"
)

// TestWorkspaceSurrealStore_RoundTrip boots a SurrealDB container, constructs
// a SurrealStore directly against it, and exercises Create / GetByID /
// ListByMember / IsMember / GetRole end-to-end, plus the EnsureDefault
// idempotency guarantee that the boot-time bootstrap relies on.
func TestWorkspaceSurrealStore_RoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testcontainers test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	surreal, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "surrealdb/surrealdb:v3.0.0",
			ExposedPorts: []string{"8000/tcp"},
			Cmd:          []string{"start", "--user", "root", "--pass", "root"},
			WaitingFor:   wait.ForListeningPort("8000/tcp").WithStartupTimeout(90 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start surrealdb: %v", err)
	}
	t.Cleanup(func() { _ = surreal.Terminate(ctx) })

	host, err := surreal.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	mapped, err := surreal.MappedPort(ctx, "8000/tcp")
	if err != nil {
		t.Fatalf("container port: %v", err)
	}

	dsn := fmt.Sprintf("ws://root:root@%s:%s/rpc/satellites/satellites", host, mapped.Port())
	cfg, err := db.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	conn, err := db.Connect(ctx, cfg)
	if err != nil {
		t.Fatalf("connect surrealdb: %v", err)
	}

	store := workspace.NewSurrealStore(conn)
	logger := satarbor.Default()
	now := time.Now().UTC()

	// Create for alice and verify creator-as-admin membership.
	a1, err := store.Create(ctx, "user_alice", "alice-alpha", now)
	if err != nil {
		t.Fatalf("Create alice-alpha: %v", err)
	}
	if a1.ID == "" || a1.Status != workspace.StatusActive {
		t.Errorf("unexpected Workspace: %+v", a1)
	}
	isAdmin, err := store.IsMember(ctx, a1.ID, "user_alice")
	if err != nil || !isAdmin {
		t.Errorf("creator should be member: is=%v err=%v", isAdmin, err)
	}
	role, err := store.GetRole(ctx, a1.ID, "user_alice")
	if err != nil || role != workspace.RoleAdmin {
		t.Errorf("role = %q err=%v, want admin", role, err)
	}

	a2, err := store.Create(ctx, "user_alice", "alice-beta", now.Add(time.Hour))
	if err != nil {
		t.Fatalf("Create alice-beta: %v", err)
	}
	b1, err := store.Create(ctx, "user_bob", "bob-only", now)
	if err != nil {
		t.Fatalf("Create bob-only: %v", err)
	}

	// GetByID round-trip.
	got, err := store.GetByID(ctx, a1.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "alice-alpha" || got.OwnerUserID != "user_alice" {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	// GetByID missing → ErrNotFound.
	if _, err := store.GetByID(ctx, "wksp_missing"); !errors.Is(err, workspace.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}

	// ListByMember scopes to caller and sorts newest-first.
	aliceList, err := store.ListByMember(ctx, "user_alice")
	if err != nil {
		t.Fatalf("ListByMember alice: %v", err)
	}
	if len(aliceList) != 2 {
		t.Fatalf("want 2 alice workspaces, got %d", len(aliceList))
	}
	if aliceList[0].ID != a2.ID || aliceList[1].ID != a1.ID {
		t.Errorf("expected newest-first: got [%s,%s]", aliceList[0].ID, aliceList[1].ID)
	}
	bobList, err := store.ListByMember(ctx, "user_bob")
	if err != nil {
		t.Fatalf("ListByMember bob: %v", err)
	}
	if len(bobList) != 1 || bobList[0].ID != b1.ID {
		t.Errorf("bob should see exactly his workspace, got %+v", bobList)
	}
	// Non-member isolation.
	crossIs, err := store.IsMember(ctx, a1.ID, "user_bob")
	if err != nil || crossIs {
		t.Errorf("bob should not be a member of alice's workspace: is=%v err=%v", crossIs, err)
	}

	// EnsureDefault idempotency across reboots — two calls for the same
	// user return the same id even after other workspaces exist.
	first, err := workspace.EnsureDefault(ctx, store, logger, "user_carol", now)
	if err != nil {
		t.Fatalf("EnsureDefault carol first: %v", err)
	}
	second, err := workspace.EnsureDefault(ctx, store, logger, "user_carol", now.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("EnsureDefault carol second: %v", err)
	}
	if first != second {
		t.Errorf("EnsureDefault not idempotent: first=%q second=%q", first, second)
	}
}
