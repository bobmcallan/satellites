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

// TestVariableVerbs exercises the variable_* surface against a live
// Postgres. Mirrors the S4 dogfood gate: project beats workspace when
// inherit=true and project_id is supplied; without project_id, the
// workspace value wins.
func TestVariableVerbs(t *testing.T) {
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

	mkWorkspaceProject := func(t *testing.T, owner *auth.User, wsName string) (wsID, pjID string) {
		t.Helper()
		ws, err := wsStore.Create(context.Background(), owner.ID, wsName, time.Now())
		if err != nil {
			t.Fatalf("create ws: %v", err)
		}
		if err := wsStore.AddMember(context.Background(), ws.ID, owner.ID, workspace.RoleAdmin, owner.ID, time.Now()); err != nil {
			t.Fatalf("add member: %v", err)
		}
		pjID = "proj_" + wsName
		if _, err := env.DB.Exec(`INSERT INTO projects (id, workspace_id, name) VALUES ($1, $2, $3)`, pjID, ws.ID, wsName); err != nil {
			t.Fatalf("seed project: %v", err)
		}
		return ws.ID, pjID
	}

	t.Run("project beats workspace when inheriting", func(t *testing.T) {
		testbootstrap.Reset(t, env)
		wsID, pjID := mkWorkspaceProject(t, admin, "alpha")

		if _, err := verb.Dispatch(ctxAdmin, "variable_set", json.RawMessage(
			`{"name":"release_channel","scope":"workspace","workspace_id":"`+wsID+`","value":"beta"}`)); err != nil {
			t.Fatalf("set workspace: %v", err)
		}
		if _, err := verb.Dispatch(ctxAdmin, "variable_set", json.RawMessage(
			`{"name":"release_channel","scope":"project","workspace_id":"`+wsID+`","project_id":"`+pjID+`","value":"stable"}`)); err != nil {
			t.Fatalf("set project: %v", err)
		}

		// With project_id and inherit=true → project wins.
		raw, err := verb.Dispatch(ctxAdmin, "variable_get", json.RawMessage(
			`{"name":"release_channel","scope":"project","workspace_id":"`+wsID+`","project_id":"`+pjID+`","inherit":true}`))
		if err != nil {
			t.Fatalf("get project: %v", err)
		}
		var got verb.VariableGetResponse
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got.Value != "stable" || got.ResolvedScope != "project" {
			t.Fatalf("project resolution: got value=%s scope=%s want stable/project", got.Value, got.ResolvedScope)
		}

		// At workspace scope (no project_id) inherit=true → workspace wins (no system value).
		raw, err = verb.Dispatch(ctxAdmin, "variable_get", json.RawMessage(
			`{"name":"release_channel","scope":"workspace","workspace_id":"`+wsID+`","inherit":true}`))
		if err != nil {
			t.Fatalf("get workspace: %v", err)
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got.Value != "beta" || got.ResolvedScope != "workspace" {
			t.Fatalf("workspace resolution: got value=%s scope=%s want beta/workspace", got.Value, got.ResolvedScope)
		}
	})

	t.Run("system inherit terminator hits the computed resolver", func(t *testing.T) {
		testbootstrap.Reset(t, env)
		wsID, _ := mkWorkspaceProject(t, admin, "withsys")

		// Wire a stub computed resolver for "version".
		verb.SetSystemVariableResolver(
			func(_ context.Context, name string) (string, bool) {
				if name == "version" {
					return "v0.0.99", true
				}
				return "", false
			},
			func(context.Context) []string { return []string{"version"} },
		)
		t.Cleanup(func() { verb.SetSystemVariableResolver(nil, nil) })

		raw, err := verb.Dispatch(ctxAdmin, "variable_get", json.RawMessage(
			`{"name":"version","scope":"workspace","workspace_id":"`+wsID+`","inherit":true}`))
		if err != nil {
			t.Fatalf("get version: %v", err)
		}
		var got verb.VariableGetResponse
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got.Value != "v0.0.99" || got.ResolvedScope != "system" {
			t.Fatalf("system resolver: got value=%s scope=%s want v0.0.99/system", got.Value, got.ResolvedScope)
		}
	})

	t.Run("non-member write at workspace scope → forbidden", func(t *testing.T) {
		testbootstrap.Reset(t, env)
		wsID, _ := mkWorkspaceProject(t, admin, "private")
		_, err := verb.Dispatch(ctxUser, "variable_set", json.RawMessage(
			`{"name":"x","scope":"workspace","workspace_id":"`+wsID+`","value":"y"}`))
		if !errors.Is(err, verb.ErrForbidden) {
			t.Fatalf("expected ErrForbidden, got %v", err)
		}
	})

	t.Run("delete + missing → NotFound", func(t *testing.T) {
		testbootstrap.Reset(t, env)
		wsID, _ := mkWorkspaceProject(t, admin, "delta")
		if _, err := verb.Dispatch(ctxAdmin, "variable_set", json.RawMessage(
			`{"name":"x","scope":"workspace","workspace_id":"`+wsID+`","value":"y"}`)); err != nil {
			t.Fatalf("set: %v", err)
		}
		if _, err := verb.Dispatch(ctxAdmin, "variable_delete", json.RawMessage(
			`{"name":"x","scope":"workspace","workspace_id":"`+wsID+`"}`)); err != nil {
			t.Fatalf("delete: %v", err)
		}
		_, err := verb.Dispatch(ctxAdmin, "variable_delete", json.RawMessage(
			`{"name":"x","scope":"workspace","workspace_id":"`+wsID+`"}`))
		if !errors.Is(err, verb.ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("list with inherit folds layers (project shadows workspace)", func(t *testing.T) {
		testbootstrap.Reset(t, env)
		wsID, pjID := mkWorkspaceProject(t, admin, "epsilon")

		set := func(scope, ws, pj, name, value string) {
			body := `{"name":"` + name + `","scope":"` + scope + `","workspace_id":"` + ws + `","value":"` + value + `"`
			if pj != "" {
				body += `,"project_id":"` + pj + `"`
			}
			body += `}`
			if _, err := verb.Dispatch(ctxAdmin, "variable_set", json.RawMessage(body)); err != nil {
				t.Fatalf("set %s/%s: %v", scope, name, err)
			}
		}
		set("workspace", wsID, "", "a", "wsA")
		set("workspace", wsID, "", "b", "wsB")
		set("project", wsID, pjID, "b", "pjB") // shadows workspace b
		set("project", wsID, pjID, "c", "pjC")

		raw, err := verb.Dispatch(ctxAdmin, "variable_list", json.RawMessage(
			`{"scope":"project","workspace_id":"`+wsID+`","project_id":"`+pjID+`","inherit":true}`))
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		var got verb.VariableListResponse
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}

		// Expect: a (workspace), b (project — shadows workspace), c (project).
		want := map[string]struct{ value, scope string }{
			"a": {"wsA", "workspace"},
			"b": {"pjB", "project"},
			"c": {"pjC", "project"},
		}
		if len(got.Variables) != len(want) {
			t.Fatalf("expected %d entries, got %d (%+v)", len(want), len(got.Variables), got.Variables)
		}
		for _, e := range got.Variables {
			w, ok := want[e.Name]
			if !ok {
				t.Fatalf("unexpected entry %q", e.Name)
			}
			if e.Value != w.value || e.ResolvedScope != w.scope {
				t.Fatalf("entry %q: got value=%s scope=%s want %s/%s", e.Name, e.Value, e.ResolvedScope, w.value, w.scope)
			}
		}
	})
}
