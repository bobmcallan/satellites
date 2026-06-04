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

// TestAPIKeyRoleGate exercises the api-key role gate end-to-end:
//
//  1. apikey_create defaults to executor; requesting the `reviewer` role is
//     rejected (reviewer keys are no longer minted).
//  2. apikey_list surfaces role on every row.
//  3. ledger_append rejects executor-role callers and accepts the admin user.
//  4. document_upsert ignores the status field (sty_42d13ae4); a story's
//     status moves only via the admin-authorized status_transition ledger
//     row; body-only patches stay open to executor.
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

	t.Run("requesting the reviewer role is rejected", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"project_id": pj.ID,
			"agent_name": "rogue-reviewer",
			"role":       "reviewer",
		})
		// Rejected for everyone — reviewer keys are no longer minted.
		if _, err := verb.Dispatch(userCtx, "apikey_create", body); err == nil ||
			!strings.Contains(err.Error(), "reviewer keys are no longer minted") {
			t.Fatalf("expected reviewer-rejected error for user, got %v", err)
		}
		if _, err := verb.Dispatch(adminCtx, "apikey_create", body); err == nil ||
			!strings.Contains(err.Error(), "reviewer keys are no longer minted") {
			t.Fatalf("expected reviewer-rejected error for admin, got %v", err)
		}
	})

	t.Run("apikey_list surfaces role on every row", func(t *testing.T) {
		// Mint an executor key first (reviewer keys are no longer minted), so
		// there is a row to surface.
		mint, _ := json.Marshal(map[string]any{"project_id": pj.ID, "agent_name": "list-row"})
		if _, err := verb.Dispatch(adminCtx, "apikey_create", mint); err != nil {
			t.Fatalf("seed executor key: %v", err)
		}
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

	// The executor context is a NON-admin user wielding an executor api-key —
	// the agent over MCP/exec. The admin-user bypass in requireLedgerAppendRole
	// keys on the USER role, so an executor key under a non-admin user is the
	// case the ledger gate must still refuse. The admin user is the privileged
	// status_transition writer (no minted reviewer key anymore).
	executorCtx := auth.WithAPIKeyRole(userCtx, auth.APIKeyRoleExecutor)

	t.Run("document_upsert ignores the status field", func(t *testing.T) {
		// document_upsert never moves status for an API-KEY caller — the field
		// is dropped regardless of the user's role, so even an admin user
		// wielding the CLI's executor key cannot self-accept via a raw patch.
		// Status moves only through the status_transition ledger row (the gate),
		// or the portal UI (a non-api-key JWT session, which IS allowed — that's
		// the one exception and is covered elsewhere).
		before := storyStatusByID(t, adminCtx, storyID)
		newStatus := "done"
		for _, c := range []struct {
			name string
			ctx  context.Context
		}{
			{"executor (non-admin user)", executorCtx},
			{"admin user + executor key", auth.WithAPIKeyRole(adminCtx, auth.APIKeyRoleExecutor)},
		} {
			body, _ := json.Marshal(verb.DocumentUpsertRequest{ID: storyID, Status: &newStatus})
			if _, err := verb.Dispatch(c.ctx, "document_upsert", body); err != nil {
				t.Fatalf("%s: document_upsert with a status field should succeed (field ignored), got %v", c.name, err)
			}
			if got := storyStatusByID(t, adminCtx, storyID); got != before {
				t.Fatalf("%s: status moved to %q via document_upsert; want unchanged %q", c.name, got, before)
			}
		}
	})

	t.Run("create via an api-key caller defaults status to backlog", func(t *testing.T) {
		// An api-key caller (the agent over MCP/exec) supplying a status at
		// create has it dropped; the store defaults to the workflow's initial
		// state. The agent cannot mint a story already at a terminal status
		// (sty_42d13ae4).
		st := "done"
		body, _ := json.Marshal(verb.DocumentUpsertRequest{
			Type:      "story",
			ProjectID: pj.ID,
			Name:      "api-key created story",
			Status:    &st,
		})
		raw, err := verb.Dispatch(executorCtx, "document_upsert", body)
		if err != nil {
			t.Fatalf("create story as api-key caller: %v", err)
		}
		var resp verb.DocumentUpsertResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if resp.Document.Status != "backlog" {
			t.Fatalf("api-key create status = %q, want backlog (status dropped)", resp.Document.Status)
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

	t.Run("status moves only via the status_transition ledger row", func(t *testing.T) {
		// The admin-authorized status_transition append is the sole writer of a
		// story's status (sty_42d13ae4): its to_status projects onto the row.
		payload, _ := json.Marshal(map[string]any{"from_status": "backlog", "to_status": "in_progress"})
		body, _ := json.Marshal(verb.LedgerAppendRequest{
			StoryID:     storyID,
			ProjectID:   pj.ID,
			WorkspaceID: ws.ID,
			Kind:        "status_transition",
			Body:        "backlog → in_progress",
			Payload:     payload,
		})
		if _, err := verb.Dispatch(adminCtx, "ledger_append", body); err != nil {
			t.Fatalf("admin status_transition append: %v", err)
		}
		if got := storyStatusByID(t, adminCtx, storyID); got != "in_progress" {
			t.Fatalf("status_transition did not project: status = %q, want in_progress", got)
		}
		// And an executor cannot drive that row — the ledger role gate refuses
		// any non-log kind from an executor key under a non-admin user.
		ex, _ := json.Marshal(verb.LedgerAppendRequest{
			StoryID: storyID, ProjectID: pj.ID, WorkspaceID: ws.ID,
			Kind: "status_transition", Body: "rogue", Payload: payload,
		})
		if _, err := verb.Dispatch(executorCtx, "ledger_append", ex); !errors.Is(err, verb.ErrForbidden) {
			t.Fatalf("executor status_transition should be forbidden, got %v", err)
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

	t.Run("admin user can ledger_append", func(t *testing.T) {
		body, _ := json.Marshal(verb.LedgerAppendRequest{
			StoryID: storyID,
			Kind:    ledger.KindComment,
			Body:    "admin note",
		})
		if _, err := verb.Dispatch(adminCtx, "ledger_append", body); err != nil {
			t.Fatalf("admin ledger_append failed: %v", err)
		}
	})

}

// storyStatusByID reads a story's current status via document_get (sty_42d13ae4
// tests assert status moves only via the gate's status_transition).
func storyStatusByID(t *testing.T, callerCtx context.Context, id string) string {
	t.Helper()
	body, _ := json.Marshal(verb.DocumentGetRequest{ID: id})
	raw, err := verb.Dispatch(callerCtx, "document_get", body)
	if err != nil {
		t.Fatalf("document_get %s: %v", id, err)
	}
	var resp verb.DocumentGetResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal document_get: %v", err)
	}
	return resp.Document.Status
}
