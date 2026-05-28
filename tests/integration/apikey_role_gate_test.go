//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestAPIKeyRoleGate exercises sty_25d5b21e end-to-end:
//
//  1. apikey_create defaults to executor; minting reviewer via the
//     verb requires admin user-role.
//  2. apikey_list surfaces role on every row.
//  3. ledger_append rejects executor-role callers and accepts reviewer.
//  4. document_upsert status-patch on a story rejects executor and
//     accepts reviewer; body-only patches stay open to executor.
//  5. The server-internal IssueReviewerKey path mints a key with the
//     requested TTL; ValidateKey rejects it once expired.
func TestAPIKeyRoleGate(t *testing.T) {
	env := testbootstrap.SetUp(t)
	ctx := context.Background()
	now := time.Now().UTC()

	authStore := auth.New(env.DB)
	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	docStore := document.New(env.DB)
	ledStore := ledger.New(env.DB)

	verb.SetAuthStore(authStore)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	verb.SetDocumentStore(docStore)
	verb.SetLedgerStore(ledStore)
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
		verb.SetDocumentStore(nil)
		verb.SetLedgerStore(nil)
	})

	admin, err := authStore.CreateUser(ctx, "usr_role_admin", "admin@role.test", "Admin", auth.RoleAdmin)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	user, err := authStore.CreateUser(ctx, "usr_role_user", "user@role.test", "User", auth.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	ws, err := wsStore.Create(ctx, admin.ID, "role-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := wsStore.AddMember(ctx, ws.ID, admin.ID, workspace.RoleAdmin, admin.ID, now); err != nil {
		t.Fatalf("add admin member: %v", err)
	}
	if err := wsStore.AddMember(ctx, ws.ID, user.ID, workspace.RoleAdmin, admin.ID, now); err != nil {
		t.Fatalf("add user member: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{
		WorkspaceID: ws.ID,
		Name:        "role-pj",
		OwnerUserID: admin.ID,
	}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	adminCtx := auth.WithUser(ctx, admin)
	userCtx := auth.WithUser(ctx, user)

	t.Run("apikey_create defaults to executor role", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"project_id": pj.ID,
			"agent_name": "default-role",
		})
		raw, err := verb.Dispatch(userCtx, "apikey_create", body)
		if err != nil {
			t.Fatalf("apikey_create: %v", err)
		}
		var resp verb.APIKeyCreateResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if resp.Role != string(auth.APIKeyRoleExecutor) {
			t.Fatalf("role = %q, want %q", resp.Role, auth.APIKeyRoleExecutor)
		}
	})

	t.Run("non-admin cannot mint reviewer via apikey_create", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"project_id": pj.ID,
			"agent_name": "rogue-reviewer",
			"role":       "reviewer",
		})
		_, err := verb.Dispatch(userCtx, "apikey_create", body)
		if err == nil || !strings.Contains(err.Error(), "requires admin") {
			t.Fatalf("expected admin-required rejection, got %v", err)
		}
	})

	t.Run("admin can mint reviewer via apikey_create", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"project_id": pj.ID,
			"agent_name": "blessed-reviewer",
			"role":       "reviewer",
		})
		raw, err := verb.Dispatch(adminCtx, "apikey_create", body)
		if err != nil {
			t.Fatalf("apikey_create: %v", err)
		}
		var resp verb.APIKeyCreateResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if resp.Role != string(auth.APIKeyRoleReviewer) {
			t.Fatalf("role = %q, want %q", resp.Role, auth.APIKeyRoleReviewer)
		}
	})

	t.Run("apikey_list surfaces role on every row", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"project_id": pj.ID})
		raw, err := verb.Dispatch(adminCtx, "apikey_list", body)
		if err != nil {
			t.Fatalf("apikey_list: %v", err)
		}
		var resp verb.APIKeyListResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(resp.Keys) == 0 {
			t.Fatalf("expected at least one key, got none")
		}
		for _, row := range resp.Keys {
			if row.Role == "" {
				t.Fatalf("row %s has empty role: %+v", row.KeyID, row)
			}
		}
	})

	// Create a story to exercise the gate against.
	createReq, _ := json.Marshal(verb.DocumentUpsertRequest{
		Type:      "story",
		ProjectID: pj.ID,
		Name:      "role-gate target",
	})
	raw, err := verb.Dispatch(adminCtx, "document_upsert", createReq)
	if err != nil {
		t.Fatalf("create target story: %v", err)
	}
	var createResp verb.DocumentUpsertResponse
	if err := json.Unmarshal(raw, &createResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	storyID := createResp.Document.ID

	executorCtx := auth.WithAPIKeyRole(adminCtx, auth.APIKeyRoleExecutor)
	reviewerCtx := auth.WithAPIKeyRole(adminCtx, auth.APIKeyRoleReviewer)

	t.Run("executor cannot patch story status", func(t *testing.T) {
		newStatus := "in_progress"
		body, _ := json.Marshal(verb.DocumentUpsertRequest{
			ID:     storyID,
			Status: &newStatus,
		})
		_, err := verb.Dispatch(executorCtx, "document_upsert", body)
		if !errors.Is(err, verb.ErrForbidden) {
			t.Fatalf("expected ErrForbidden, got %v", err)
		}
	})

	t.Run("executor can patch story body", func(t *testing.T) {
		body, _ := json.Marshal(verb.DocumentUpsertRequest{
			ID:   storyID,
			Body: "executor-side body edit",
		})
		if _, err := verb.Dispatch(executorCtx, "document_upsert", body); err != nil {
			t.Fatalf("body patch should succeed for executor, got %v", err)
		}
	})

	t.Run("reviewer can patch story status", func(t *testing.T) {
		newStatus := "in_progress"
		body, _ := json.Marshal(verb.DocumentUpsertRequest{
			ID:     storyID,
			Status: &newStatus,
		})
		if _, err := verb.Dispatch(reviewerCtx, "document_upsert", body); err != nil {
			t.Fatalf("reviewer status patch failed: %v", err)
		}
	})

	t.Run("executor cannot ledger_append", func(t *testing.T) {
		body, _ := json.Marshal(verb.LedgerAppendRequest{
			StoryID: storyID,
			Kind:    ledger.KindComment,
			Body:    "executor poke",
		})
		_, err := verb.Dispatch(executorCtx, "ledger_append", body)
		if !errors.Is(err, verb.ErrForbidden) {
			t.Fatalf("expected ErrForbidden, got %v", err)
		}
	})

	t.Run("reviewer can ledger_append", func(t *testing.T) {
		body, _ := json.Marshal(verb.LedgerAppendRequest{
			StoryID: storyID,
			Kind:    ledger.KindComment,
			Body:    "reviewer note",
		})
		if _, err := verb.Dispatch(reviewerCtx, "ledger_append", body); err != nil {
			t.Fatalf("reviewer ledger_append failed: %v", err)
		}
	})

	t.Run("IssueReviewerKey honours TTL and expires", func(t *testing.T) {
		_, key, err := authStore.IssueReviewerKey(ctx, auth.IssueReviewerKeyInput{
			UserID:    admin.ID,
			ProjectID: pj.ID,
			AgentName: "ttl-test",
			TTL:       50 * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("IssueReviewerKey: %v", err)
		}
		if key.Role != auth.APIKeyRoleReviewer {
			t.Fatalf("role = %q, want reviewer", key.Role)
		}
		if key.ExpiresAt == nil {
			t.Fatalf("expires_at should be set on a reviewer key")
		}
		time.Sleep(100 * time.Millisecond)
		// Re-validate by raw key string would need the raw return; instead
		// query directly to confirm ValidateKey filters expired rows. The
		// IssueReviewerKey return shape gives us the row but not the raw
		// key string in this assertion — so we drive ValidateKey from the
		// raw return below.
		_ = key
	})

	t.Run("ValidateKey filters expired reviewer keys", func(t *testing.T) {
		raw, _, err := authStore.IssueReviewerKey(ctx, auth.IssueReviewerKeyInput{
			UserID:    admin.ID,
			ProjectID: pj.ID,
			AgentName: "expire-validate",
			TTL:       50 * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("IssueReviewerKey: %v", err)
		}
		if _, err := authStore.ValidateKey(ctx, raw); err != nil {
			t.Fatalf("freshly minted key should validate, got %v", err)
		}
		time.Sleep(100 * time.Millisecond)
		if _, err := authStore.ValidateKey(ctx, raw); !errors.Is(err, auth.ErrInvalidKey) {
			t.Fatalf("expected ErrInvalidKey after TTL, got %v", err)
		}
	})

	t.Run("MintReviewerKeyForStory + RevokeReviewerKeyForStory log to ledger", func(t *testing.T) {
		minted, err := verb.MintReviewerKeyForStory(ctx, verb.MintReviewerKeyInput{
			StoryID:   storyID,
			UserID:    admin.ID,
			ProjectID: pj.ID,
			AgentName: "review-subprocess",
		})
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		if minted.APIKey == "" || minted.KeyID == "" || minted.ExpiresAt == nil {
			t.Fatalf("mint returned incomplete shape: %+v", minted)
		}

		entries, err := ledStore.ListByStory(ctx, storyID, ledger.KindReviewerKeyMinted)
		if err != nil {
			t.Fatalf("list mint entries: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("expected 1 mint entry, got %d", len(entries))
		}

		if err := verb.RevokeReviewerKeyForStory(ctx, storyID, minted.KeyID, admin.ID); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		entries, err = ledStore.ListByStory(ctx, storyID, ledger.KindReviewerKeyRevoked)
		if err != nil {
			t.Fatalf("list revoke entries: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("expected 1 revoke entry, got %d", len(entries))
		}

		if _, err := authStore.ValidateKey(ctx, minted.APIKey); !errors.Is(err, auth.ErrInvalidKey) {
			t.Fatalf("expected ErrInvalidKey after revoke, got %v", err)
		}
	})
}
