//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestDocumentUserScope exercises the user-override cascade layer end-to-end
// (sty_cbeeb452): a user-scope row shadows the same-named project/system row
// for that caller, document_get returns the effective body with
// resolved_scope=user, document_list effective overlays it, removing the
// override restores the default, and one user's override is invisible to
// another.
func TestDocumentUserScope(t *testing.T) {
	env := testbootstrap.SetUp(t)

	docStore := document.New(env.DB)
	authStore := auth.New(env.DB)
	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)

	verb.SetDocumentStore(docStore)
	verb.SetAuthStore(authStore)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	t.Cleanup(func() {
		verb.SetDocumentStore(nil)
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
	})

	if _, err := env.DB.Exec(`TRUNCATE api_keys, users, workspace_members RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate auth: %v", err)
	}
	if err := authStore.DevSeed(context.Background()); err != nil {
		t.Fatalf("dev seed: %v", err)
	}
	admin, err := authStore.GetUserByEmail(context.Background(), auth.DevAdminEmail)
	if err != nil {
		t.Fatalf("lookup admin: %v", err)
	}
	user, err := authStore.GetUserByEmail(context.Background(), auth.DevUserEmail)
	if err != nil {
		t.Fatalf("lookup user: %v", err)
	}
	ctxAdmin := authWithUser(context.Background(), admin)
	ctxUser := authWithUser(context.Background(), user)

	// A workspace + project the admin belongs to, so project-scope rows pass
	// the membership check and the FK constraints.
	ws, err := wsStore.Create(context.Background(), admin.ID, "U", time.Now())
	if err != nil {
		t.Fatalf("create ws: %v", err)
	}
	if err := wsStore.AddMember(context.Background(), ws.ID, admin.ID, workspace.RoleAdmin, admin.ID, time.Now()); err != nil {
		t.Fatalf("add admin: %v", err)
	}
	if err := wsStore.AddMember(context.Background(), ws.ID, user.ID, workspace.RoleMember, admin.ID, time.Now()); err != nil {
		t.Fatalf("add user: %v", err)
	}
	if _, err := env.DB.Exec(`INSERT INTO projects (id, workspace_id, name) VALUES ($1, $2, 'p')`, "proj_user", ws.ID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	// Under project-scoped RBAC (sty_c96cc77f) workspace membership no longer
	// implies project access, so the non-admin user needs an explicit project
	// read grant to read proj_user. (admin is a global admin → implicit admin.)
	if err := pjStore.AddMember(context.Background(), "proj_user", user.ID, project.RoleRead, admin.ID, time.Now()); err != nil {
		t.Fatalf("grant user project read: %v", err)
	}

	// Seed the three layers for name "cfg": system + project + admin's user
	// override. (workspace is intentionally absent.)
	if err := document.SeedSystem(context.Background(), docStore, "cfg", "system-body", "system:seed", time.Now()); err != nil {
		t.Fatalf("seed system: %v", err)
	}
	if _, _, err := docStore.Upsert(context.Background(), document.UpsertInput{
		Key:  document.Key{Scope: document.ScopeProject, WorkspaceID: ws.ID, ProjectID: "proj_user", Name: "cfg"},
		Body: "project-body",
	}, time.Now()); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	userKey := document.Key{Scope: document.ScopeUser, UserID: admin.ID, Name: "cfg"}
	if _, _, err := docStore.Upsert(context.Background(), document.UpsertInput{Key: userKey, Body: "user-body"}, time.Now()); err != nil {
		t.Fatalf("upsert user override: %v", err)
	}

	getEffective := func(t *testing.T, ctx context.Context) verb.DocumentGetResponse {
		t.Helper()
		raw, err := verb.Dispatch(ctx, "document_get", json.RawMessage(
			`{"name":"cfg","scope":"project","workspace_id":"`+ws.ID+`","project_id":"proj_user","inherit":true}`))
		if err != nil {
			t.Fatalf("get cfg: %v", err)
		}
		var got verb.DocumentGetResponse
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	t.Run("user shadows project shadows system", func(t *testing.T) {
		got := getEffective(t, ctxAdmin)
		if got.ResolvedScope != "user" {
			t.Fatalf("resolved_scope=%s want user", got.ResolvedScope)
		}
		if got.Versions[0].Body != "user-body" {
			t.Fatalf("body=%q want user-body", got.Versions[0].Body)
		}
	})

	t.Run("another user does not see the override (isolation)", func(t *testing.T) {
		got := getEffective(t, ctxUser)
		if got.ResolvedScope != "project" {
			t.Fatalf("resolved_scope=%s want project (admin override invisible to user)", got.ResolvedScope)
		}
		if got.Versions[0].Body != "project-body" {
			t.Fatalf("body=%q want project-body", got.Versions[0].Body)
		}
	})

	t.Run("document_list effective overlays the override per caller", func(t *testing.T) {
		listScope := func(ctx context.Context) string {
			raw, err := verb.Dispatch(ctx, "document_list", json.RawMessage(
				`{"type":"document","scope":"project","workspace_id":"`+ws.ID+`","project_id":"proj_user","name_prefix":"cfg","effective":true}`))
			if err != nil {
				t.Fatalf("list effective: %v", err)
			}
			var resp verb.DocumentListResponse
			if err := json.Unmarshal(raw, &resp); err != nil {
				t.Fatal(err)
			}
			for _, it := range resp.Items {
				if it.Name == "cfg" {
					return string(it.Scope)
				}
			}
			t.Fatalf("cfg not present in effective list: %+v", resp.Items)
			return ""
		}
		if s := listScope(ctxAdmin); s != "user" {
			t.Fatalf("admin effective list cfg scope=%s want user", s)
		}
		if s := listScope(ctxUser); s != "project" {
			t.Fatalf("user effective list cfg scope=%s want project", s)
		}
	})

	t.Run("removing the override restores the project default", func(t *testing.T) {
		if _, _, err := docStore.Delete(context.Background(), userKey, admin.ID, false, time.Now()); err != nil {
			t.Fatalf("delete override: %v", err)
		}
		got := getEffective(t, ctxAdmin)
		if got.ResolvedScope != "project" {
			t.Fatalf("after removing override resolved_scope=%s want project", got.ResolvedScope)
		}
		if got.Versions[0].Body != "project-body" {
			t.Fatalf("body=%q want project-body", got.Versions[0].Body)
		}
	})

	t.Run("user override authored through the verb is keyed to the caller", func(t *testing.T) {
		// user (not admin) authors their own override via document_upsert.
		if _, err := verb.Dispatch(ctxUser, "document_upsert", json.RawMessage(
			`{"type":"document","scope":"user","name":"cfg","body":"user2-body"}`)); err != nil {
			t.Fatalf("user upsert override: %v", err)
		}
		// user now resolves their own override.
		got := getEffective(t, ctxUser)
		if got.ResolvedScope != "user" || got.Versions[0].Body != "user2-body" {
			t.Fatalf("user override not effective: scope=%s body=%q", got.ResolvedScope, got.Versions[0].Body)
		}
		// admin (override already removed above) still resolves the project row,
		// proving the verb-authored override is keyed to user, not shared.
		gotAdmin := getEffective(t, ctxAdmin)
		if gotAdmin.ResolvedScope != "project" {
			t.Fatalf("admin sees user's override: resolved_scope=%s want project", gotAdmin.ResolvedScope)
		}
		// A direct cross-user read of another user's scope is forbidden.
		_, err := verb.Dispatch(ctxAdmin, "document_get", json.RawMessage(
			`{"name":"cfg","scope":"user"}`))
		// admin has no user-scope cfg row of their own → NotFound, never the
		// other user's body.
		if err == nil || !errors.Is(err, verb.ErrNotFound) {
			t.Fatalf("admin direct user-scope read = %v; want NotFound (own empty layer)", err)
		}
	})
}
