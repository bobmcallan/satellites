//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/variable"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestDocumentGet_TemplateRendering exercises the S5 dogfood gate:
//
//	upsert a project-scoped document body 'binary={{version}} platform={{os}}/{{arch}}';
//	document_get against a session reporting os=linux arch=amd64 returns
//	the rendered string with the running server's verb.Version filled in.
func TestDocumentGet_TemplateRendering(t *testing.T) {
	env := testbootstrap.SetUp(t)

	docStore := document.New(env.DB)
	varStore := variable.New(env.DB)
	authStore := auth.New(env.DB)
	wsStore := workspace.New(env.DB)

	verb.SetDocumentStore(docStore)
	verb.SetVariableStore(varStore)
	verb.SetAuthStore(authStore)
	verb.SetWorkspaceStore(wsStore)
	t.Cleanup(func() {
		verb.SetDocumentStore(nil)
		verb.SetVariableStore(nil)
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
		verb.SetSystemVariableResolver(nil, nil)
	})

	// Same shape as the satellites-server boot wiring.
	systemVars := map[string]func(ctx context.Context) string{
		"version":         func(context.Context) string { return verb.Version },
		"cli_version":     func(context.Context) string { return verb.CLIVersionEffective() },
		"os":              func(ctx context.Context) string { return verb.OSFromContext(ctx) },
		"arch":            func(ctx context.Context) string { return verb.ArchFromContext(ctx) },
		"server_url":      func(context.Context) string { return "https://test.example" },
		"current_version": func(ctx context.Context) string { return verb.CurrentVersionFromContext(ctx) },
		"state": func(ctx context.Context) string {
			return verb.ComputeInstallState(verb.CurrentVersionFromContext(ctx), verb.CLIVersionEffective())
		},
	}
	verb.SetSystemVariableResolver(
		func(ctx context.Context, name string) (string, bool) {
			fn, ok := systemVars[name]
			if !ok {
				return "", false
			}
			return fn(ctx), true
		},
		func(context.Context) []string {
			return []string{"version", "cli_version", "os", "arch", "server_url", "current_version", "state"}
		},
	)

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
	ctxAdmin := authWithUser(context.Background(), admin)

	ws, err := wsStore.Create(context.Background(), admin.ID, "tmpl", time.Now())
	if err != nil {
		t.Fatalf("create ws: %v", err)
	}
	if err := wsStore.AddMember(context.Background(), ws.ID, admin.ID, workspace.RoleAdmin, admin.ID, time.Now()); err != nil {
		t.Fatalf("add admin: %v", err)
	}
	pjID := "proj_tmpl"
	if _, err := env.DB.Exec(`INSERT INTO projects (id, workspace_id, name) VALUES ($1, $2, 'tmpl')`, pjID, ws.ID); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	// Upsert a project-scoped doc with system-var placeholders.
	if _, err := verb.Dispatch(ctxAdmin, "document_upsert", json.RawMessage(
		`{"name":"install_card","scope":"project","workspace_id":"`+ws.ID+`","project_id":"`+pjID+`","body":"binary={{version}} platform={{os}}/{{arch}}"}`,
	)); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	t.Run("system vars render from request fields", func(t *testing.T) {
		raw, err := verb.Dispatch(ctxAdmin, "document_get", json.RawMessage(
			`{"name":"install_card","scope":"project","workspace_id":"`+ws.ID+`","project_id":"`+pjID+`","os":"linux","arch":"amd64"}`,
		))
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		var got verb.DocumentGetResponse
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		want := "binary=" + verb.Version + " platform=linux/amd64"
		if got.RenderedBody != want {
			t.Fatalf("rendered_body: got %q want %q", got.RenderedBody, want)
		}
		if got.RawBody != "binary={{version}} platform={{os}}/{{arch}}" {
			t.Fatalf("raw_body diverged: %q", got.RawBody)
		}
		if len(got.UnresolvedVars) != 0 {
			t.Fatalf("unexpected unresolved: %+v", got.UnresolvedVars)
		}
	})

	t.Run("unresolved variables surface without failing the call", func(t *testing.T) {
		if _, err := verb.Dispatch(ctxAdmin, "document_upsert", json.RawMessage(
			`{"name":"sparse","scope":"project","workspace_id":"`+ws.ID+`","project_id":"`+pjID+`","body":"hi {{unset_var}} bye"}`,
		)); err != nil {
			t.Fatalf("upsert: %v", err)
		}
		raw, err := verb.Dispatch(ctxAdmin, "document_get", json.RawMessage(
			`{"name":"sparse","scope":"project","workspace_id":"`+ws.ID+`","project_id":"`+pjID+`"}`,
		))
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		var got verb.DocumentGetResponse
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got.RenderedBody != "hi {{unset_var}} bye" {
			t.Fatalf("rendered: %q", got.RenderedBody)
		}
		if len(got.UnresolvedVars) != 1 || got.UnresolvedVars[0] != "unset_var" {
			t.Fatalf("unresolved: %+v", got.UnresolvedVars)
		}
	})

	t.Run("system vars take precedence over operator-set variables", func(t *testing.T) {
		// Operator sets a workspace variable named "version" with a value
		// that should NOT shadow the system version.
		if _, err := verb.Dispatch(ctxAdmin, "variable_set", json.RawMessage(
			`{"name":"version","scope":"workspace","workspace_id":"`+ws.ID+`","value":"OPERATOR-OVERRIDE"}`,
		)); err != nil {
			t.Fatalf("set var: %v", err)
		}
		raw, err := verb.Dispatch(ctxAdmin, "document_get", json.RawMessage(
			`{"name":"install_card","scope":"project","workspace_id":"`+ws.ID+`","project_id":"`+pjID+`","os":"linux","arch":"amd64"}`,
		))
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		var got verb.DocumentGetResponse
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(got.RenderedBody, "OPERATOR-OVERRIDE") {
			t.Fatalf("operator var leaked through system layer: %q", got.RenderedBody)
		}
	})

	t.Run("operator variables resolve when there is no system var of that name", func(t *testing.T) {
		// Set a project-scoped variable; rendering should pick it up.
		if _, err := verb.Dispatch(ctxAdmin, "variable_set", json.RawMessage(
			`{"name":"flavor","scope":"project","workspace_id":"`+ws.ID+`","project_id":"`+pjID+`","value":"vanilla"}`,
		)); err != nil {
			t.Fatalf("set: %v", err)
		}
		if _, err := verb.Dispatch(ctxAdmin, "document_upsert", json.RawMessage(
			`{"name":"flavored","scope":"project","workspace_id":"`+ws.ID+`","project_id":"`+pjID+`","body":"flavor={{flavor}}"}`,
		)); err != nil {
			t.Fatalf("upsert: %v", err)
		}
		raw, err := verb.Dispatch(ctxAdmin, "document_get", json.RawMessage(
			`{"name":"flavored","scope":"project","workspace_id":"`+ws.ID+`","project_id":"`+pjID+`"}`,
		))
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		var got verb.DocumentGetResponse
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got.RenderedBody != "flavor=vanilla" {
			t.Fatalf("rendered: %q", got.RenderedBody)
		}
	})

	t.Run("version=all does not render", func(t *testing.T) {
		raw, err := verb.Dispatch(ctxAdmin, "document_get", json.RawMessage(
			`{"name":"install_card","scope":"project","workspace_id":"`+ws.ID+`","project_id":"`+pjID+`","version":"all"}`,
		))
		if err != nil {
			t.Fatalf("get all: %v", err)
		}
		var got verb.DocumentGetResponse
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got.RenderedBody != "" || got.RawBody != "" {
			t.Fatalf("version=all should not render: rendered=%q raw=%q", got.RenderedBody, got.RawBody)
		}
	})
}
