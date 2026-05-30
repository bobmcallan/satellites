//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestAPIKeyVerbs_EndToEnd exercises the bootstrap closure:
// authenticated caller mints a project-scoped key via apikey_create,
// lists it via apikey_list, revokes it via apikey_revoke, and
// confirms a second caller (non-member) is rejected by the membership
// check. Together these cover AC #1-#4 + #7 from sty_3b22918c.
func TestAPIKeyVerbs_EndToEnd(t *testing.T) {
	env := testbootstrap.SetUp(t)
	ctx := context.Background()
	now := time.Now().UTC()

	store := auth.New(env.DB)
	owner, err := store.CreateUser(ctx, "usr_apikey_owner", "owner@example.com", "Owner", auth.RoleUser)
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	outsider, err := store.CreateUser(ctx, "usr_apikey_outsider", "outsider@example.com", "Outsider", auth.RoleUser)
	if err != nil {
		t.Fatalf("create outsider: %v", err)
	}

	wsStore := workspace.New(env.DB)
	ws, err := wsStore.Create(ctx, owner.ID, "apikey-test-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	pjStore := project.New(env.DB)
	pj, err := pjStore.Create(ctx, project.CreateInput{
		WorkspaceID: ws.ID,
		Name:        "apikey-test-project",
		OwnerUserID: owner.ID,
	}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	verb.SetAuthStore(store)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
	})

	ownerCtx := auth.WithUser(ctx, owner)
	outsiderCtx := auth.WithUser(ctx, outsider)

	t.Run("outsider cannot mint a key for a workspace they don't belong to", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"workspace_id": ws.ID,
			"project_id":   pj.ID,
			"agent_name":   "intruder",
		})
		if _, err := verb.Dispatch(outsiderCtx, "apikey_create", body); err == nil ||
			!strings.Contains(err.Error(), "forbidden") {
			t.Fatalf("expected forbidden for outsider, got %v", err)
		}
	})

	var minted verb.APIKeyCreateResponse
	t.Run("owner mints a project-scoped key", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"workspace_id": ws.ID,
			"project_id":   pj.ID,
			"agent_name":   "test-agent",
			"scopes":       []string{"document:read"},
		})
		raw, err := verb.Dispatch(ownerCtx, "apikey_create", body)
		if err != nil {
			t.Fatalf("apikey_create: %v", err)
		}
		if err := json.Unmarshal(raw, &minted); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !strings.HasPrefix(minted.APIKey, "sk_") {
			t.Fatalf("expected sk_-prefixed raw key, got %q", minted.APIKey)
		}
		if minted.KeyID == "" || minted.ProjectID != pj.ID || minted.WorkspaceID != ws.ID || minted.AgentName != "test-agent" {
			t.Fatalf("unexpected response shape: %+v", minted)
		}
		if minted.CreatedAt.IsZero() {
			t.Fatalf("created_at unset")
		}
		if minted.Role != string(auth.APIKeyRoleExecutor) {
			t.Fatalf("default role = %q, want %q", minted.Role, auth.APIKeyRoleExecutor)
		}
	})

	t.Run("the minted key validates as the owner", func(t *testing.T) {
		u, err := store.ValidateKey(ctx, minted.APIKey)
		if err != nil {
			t.Fatalf("validate: %v", err)
		}
		if u.ID != owner.ID {
			t.Fatalf("validate returned %q, want %q", u.ID, owner.ID)
		}
	})

	t.Run("apikey_list returns the minted key (filtered by project)", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"project_id": pj.ID})
		raw, err := verb.Dispatch(ownerCtx, "apikey_list", body)
		if err != nil {
			t.Fatalf("apikey_list: %v", err)
		}
		var resp verb.APIKeyListResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(resp.Keys) != 1 || resp.Keys[0].KeyID != minted.KeyID {
			t.Fatalf("expected exactly [%s], got %+v", minted.KeyID, resp.Keys)
		}
	})

	t.Run("apikey_list scoped to a different project returns empty", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"project_id": "proj_does_not_exist"})
		raw, err := verb.Dispatch(ownerCtx, "apikey_list", body)
		if err != nil {
			t.Fatalf("apikey_list: %v", err)
		}
		var resp verb.APIKeyListResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(resp.Keys) != 0 {
			t.Fatalf("expected empty list, got %+v", resp.Keys)
		}
	})

	t.Run("outsider cannot revoke the owner's key", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"key_id": minted.KeyID})
		if _, err := verb.Dispatch(outsiderCtx, "apikey_revoke", body); err == nil ||
			!strings.Contains(err.Error(), "not found or not yours") {
			t.Fatalf("expected ownership rejection, got %v", err)
		}
	})

	t.Run("owner revokes and the key stops validating", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"key_id": minted.KeyID})
		if _, err := verb.Dispatch(ownerCtx, "apikey_revoke", body); err != nil {
			t.Fatalf("apikey_revoke: %v", err)
		}
		if _, err := store.ValidateKey(ctx, minted.APIKey); err == nil {
			t.Fatalf("expected ValidateKey to reject revoked key, got nil error")
		}
	})
}

// TestReviewerKeyMint_RotatesSlot pins sty_e2dea1ec: the client-side gate
// mints a fresh reviewer key on every run. The api_keys_project_agent
// unique index counts a prior run's row even after it is revoked or
// expired, so a blind insert collides on the second review per
// (user, project) — exactly what blocked the loop on reuse. The reviewer
// mint must rotate its single reserved slot in place instead.
func TestReviewerKeyMint_RotatesSlot(t *testing.T) {
	env := testbootstrap.SetUp(t)
	ctx := context.Background()
	now := time.Now().UTC()

	store := auth.New(env.DB)
	owner, err := store.CreateUser(ctx, "usr_rev_owner", "rev-owner@example.com", "Rev Owner", auth.RoleUser)
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	wsStore := workspace.New(env.DB)
	ws, err := wsStore.Create(ctx, owner.ID, "rev-test-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pjStore := project.New(env.DB)
	pj, err := pjStore.Create(ctx, project.CreateInput{
		WorkspaceID: ws.ID,
		Name:        "rev-test-project",
		OwnerUserID: owner.ID,
	}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	// First reviewer mint — the reserved slot is empty, so a plain insert.
	raw1, k1, err := store.IssueReviewerKey(ctx, auth.IssueReviewerKeyInput{
		UserID: owner.ID, ProjectID: pj.ID,
	})
	if err != nil {
		t.Fatalf("first reviewer mint: %v", err)
	}

	// The gate revokes its key on teardown (best-effort). Revocation leaves
	// the row in place — the index still counts it.
	if err := store.RevokeKey(ctx, k1.ID); err != nil {
		t.Fatalf("revoke first reviewer key: %v", err)
	}

	// Second mint for the SAME (user, project) must succeed by rotating the
	// slot in place — not collide on api_keys_project_agent. Against the old
	// blind insert this is the line that failed.
	raw2, _, err := store.IssueReviewerKey(ctx, auth.IssueReviewerKeyInput{
		UserID: owner.ID, ProjectID: pj.ID,
	})
	if err != nil {
		t.Fatalf("second reviewer mint must rotate the slot, got: %v", err)
	}

	// The rotated key validates; the revoked first key does not.
	if _, err := store.ValidateKey(ctx, raw2); err != nil {
		t.Fatalf("rotated reviewer key should validate: %v", err)
	}
	if _, err := store.ValidateKey(ctx, raw1); err == nil {
		t.Fatalf("revoked/rotated first reviewer key should no longer validate")
	}

	// Exactly one reviewer row per (user, project) — rotate-in-place, no
	// accumulation of dead rows.
	var count int
	if err := env.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM api_keys WHERE user_id=$1 AND project_id=$2 AND role='reviewer'`,
		owner.ID, pj.ID).Scan(&count); err != nil {
		t.Fatalf("count reviewer rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 reviewer row after rotate, got %d", count)
	}
}
