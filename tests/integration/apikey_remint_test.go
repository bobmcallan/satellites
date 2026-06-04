//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestExecutorKeyRemint covers sty_03d714c2: a project-scoped executor
// (bootstrap) key must be re-mintable under the same agent_name — the
// collision vire's bootstrap hit. Re-mint rotates the (user, project,
// agent_name) slot in place, so revoke→re-mint succeeds and there is never
// more than one row for the slot (no accumulation of dead rows).
func TestExecutorKeyRemint(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)
	ctx := context.Background()
	now := time.Now().UTC()

	store := auth.New(env.DB)
	owner, err := store.CreateUser(ctx, "usr_remint_owner", "remint-owner@example.com", "Remint Owner", auth.RoleUser)
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	ws, err := workspace.New(env.DB).Create(ctx, owner.ID, "remint-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pj, err := project.New(env.DB).Create(ctx, project.CreateInput{
		WorkspaceID: ws.ID,
		Name:        "remint-project",
		OwnerUserID: owner.ID,
	}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	const agent = "bootstrap"

	// First bootstrap mint — empty slot, plain insert.
	raw1, k1, err := store.IssueAPIKey(ctx, "apk_remint_1", owner.ID, pj.ID, agent)
	if err != nil {
		t.Fatalf("first executor mint: %v", err)
	}

	// Revoke it (what `apikey_revoke` does on a re-bootstrap).
	if err := store.RevokeKey(ctx, k1.ID); err != nil {
		t.Fatalf("revoke first key: %v", err)
	}

	// Re-mint under the SAME (user, project, agent_name) — against the old
	// blind insert this collided on api_keys_project_agent and forced a
	// renamed agent. It must now succeed by rotating the slot.
	raw2, _, err := store.IssueAPIKey(ctx, "apk_remint_2", owner.ID, pj.ID, agent)
	if err != nil {
		t.Fatalf("re-mint after revoke must succeed, got: %v", err)
	}

	// The new key validates; the revoked/rotated first key does not (AC1).
	if _, err := store.ValidateKey(ctx, raw2); err != nil {
		t.Fatalf("re-minted key should validate: %v", err)
	}
	if _, err := store.ValidateKey(ctx, raw1); err == nil {
		t.Fatalf("revoked first key should no longer validate")
	}

	// Re-mint AGAIN without revoking — rotates the active slot in place, so
	// there is never more than one row per slot (AC2: no two active keys).
	raw3, _, err := store.IssueAPIKey(ctx, "apk_remint_3", owner.ID, pj.ID, agent)
	if err != nil {
		t.Fatalf("re-mint of an active slot must rotate, got: %v", err)
	}
	if _, err := store.ValidateKey(ctx, raw3); err != nil {
		t.Fatalf("rotated key should validate: %v", err)
	}
	if _, err := store.ValidateKey(ctx, raw2); err == nil {
		t.Fatalf("superseded key should no longer validate after rotate")
	}

	var count int
	if err := env.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM api_keys WHERE user_id=$1 AND project_id=$2 AND agent_name=$3`,
		owner.ID, pj.ID, agent).Scan(&count); err != nil {
		t.Fatalf("count slot rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 row for the (user,project,agent) slot, got %d", count)
	}
}
