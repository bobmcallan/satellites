//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/variable"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestVariableUserScope exercises the user-override layer end-to-end against a
// live Postgres (sty_6cdf1cd0 AC5 dogfood): the same key set at user and
// project scope resolves to the user value with inherit=true; deleting the user
// value falls through to the project value. Also confirms the user scope is
// owner-only — a different caller never sees another user's override.
func TestVariableUserScope(t *testing.T) {
	env := testbootstrap.SetUp(t)

	varStore := variable.New(env.DB)
	authStore := auth.New(env.DB)
	wsStore := workspace.New(env.DB)

	verb.SetVariableStore(varStore)
	verb.SetAuthStore(authStore)
	verb.SetWorkspaceStore(wsStore)
	t.Cleanup(func() {
		verb.SetVariableStore(nil)
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
	})

	if _, err := env.DB.Exec(`TRUNCATE api_keys, users, workspace_members RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate auth: %v", err)
	}
	if err := authStore.DevSeed(context.Background()); err != nil {
		t.Fatalf("dev seed: %v", err)
	}
	user, err := authStore.GetUserByEmail(context.Background(), auth.DevUserEmail)
	if err != nil {
		t.Fatalf("lookup user: %v", err)
	}
	admin, err := authStore.GetUserByEmail(context.Background(), auth.DevAdminEmail)
	if err != nil {
		t.Fatalf("lookup admin: %v", err)
	}
	ctxUser := authWithUser(context.Background(), user)
	ctxAdmin := authWithUser(context.Background(), admin)

	// Workspace + project owned by `user`, so the user is a member and may
	// read/write its project-scope variables.
	ws, err := wsStore.Create(context.Background(), user.ID, "userscope", time.Now())
	if err != nil {
		t.Fatalf("create ws: %v", err)
	}
	if err := wsStore.AddMember(context.Background(), ws.ID, user.ID, workspace.RoleAdmin, user.ID, time.Now()); err != nil {
		t.Fatalf("add member: %v", err)
	}
	pjID := "proj_userscope"
	if _, err := env.DB.Exec(`INSERT INTO projects (id, workspace_id, name) VALUES ($1, $2, $3)`, pjID, ws.ID, "userscope"); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	getProjectInherit := func(t *testing.T, ctx context.Context) verb.VariableGetResponse {
		t.Helper()
		raw, err := verb.Dispatch(ctx, "variable_get", json.RawMessage(
			`{"name":"workflow.max_iterations","scope":"project","workspace_id":"`+ws.ID+`","project_id":"`+pjID+`","inherit":true}`))
		if err != nil {
			t.Fatalf("get project inherit: %v", err)
		}
		var got verb.VariableGetResponse
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	// Project value present.
	if _, err := verb.Dispatch(ctxUser, "variable_set", json.RawMessage(
		`{"name":"workflow.max_iterations","scope":"project","workspace_id":"`+ws.ID+`","project_id":"`+pjID+`","value":"3"}`)); err != nil {
		t.Fatalf("set project: %v", err)
	}
	// User override on top.
	if _, err := verb.Dispatch(ctxUser, "variable_set", json.RawMessage(
		`{"name":"workflow.max_iterations","scope":"user","value":"7"}`)); err != nil {
		t.Fatalf("set user: %v", err)
	}

	// inherit=true → user override wins.
	if got := getProjectInherit(t, ctxUser); got.Value != "7" || got.ResolvedScope != "user" {
		t.Fatalf("with user override: got value=%s scope=%s want 7/user", got.Value, got.ResolvedScope)
	}

	// Delete the user override → project value resolves.
	if _, err := verb.Dispatch(ctxUser, "variable_delete", json.RawMessage(
		`{"name":"workflow.max_iterations","scope":"user"}`)); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if got := getProjectInherit(t, ctxUser); got.Value != "3" || got.ResolvedScope != "project" {
		t.Fatalf("after delete: got value=%s scope=%s want 3/project", got.Value, got.ResolvedScope)
	}

	t.Run("user scope is owner-only", func(t *testing.T) {
		// user re-sets the override; admin (a different caller) never sees it
		// — its user cascade resolves only admin's own rows.
		if _, err := verb.Dispatch(ctxUser, "variable_set", json.RawMessage(
			`{"name":"workflow.max_iterations","scope":"user","value":"7"}`)); err != nil {
			t.Fatalf("set user: %v", err)
		}
		raw, err := verb.Dispatch(ctxAdmin, "variable_get", json.RawMessage(
			`{"name":"workflow.max_iterations","scope":"user","inherit":false}`))
		if err == nil {
			t.Fatalf("admin should not resolve the user's override, got %s", string(raw))
		}
		if !errors.Is(err, verb.ErrNotFound) {
			t.Fatalf("expected ErrNotFound for admin's empty user scope, got %v", err)
		}
	})

	t.Run("user write requires a caller identity", func(t *testing.T) {
		_, err := verb.Dispatch(context.Background(), "variable_set", json.RawMessage(
			`{"name":"k","scope":"user","value":"v"}`))
		if !errors.Is(err, verb.ErrBadRequest) {
			t.Fatalf("expected ErrBadRequest for no-caller user write, got %v", err)
		}
	})

	t.Run("list at project inherit folds the user override", func(t *testing.T) {
		raw, err := verb.Dispatch(ctxUser, "variable_list", json.RawMessage(
			`{"scope":"project","workspace_id":"`+ws.ID+`","project_id":"`+pjID+`","inherit":true}`))
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		var got verb.VariableListResponse
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		var found bool
		for _, e := range got.Variables {
			if e.Name == "workflow.max_iterations" {
				found = true
				if e.Value != "7" || e.ResolvedScope != "user" {
					t.Fatalf("list entry: got value=%s scope=%s want 7/user", e.Value, e.ResolvedScope)
				}
			}
		}
		if !found {
			t.Fatalf("user override not folded into the project list (%+v)", got.Variables)
		}
	})
}
