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
