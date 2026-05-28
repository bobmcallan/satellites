//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/config/documents"
	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/variable"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestSystemVariablesDoc_DogfoodFlow exercises the S8 dogfood gate:
// an agent reading only the MCP load context + document_get(name=
// system_variables) can author a templated workspace-scoped document
// that references {{server_url}} and have it render correctly when
// retrieved.
func TestSystemVariablesDoc_DogfoodFlow(t *testing.T) {
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

	verb.SetSystemVariableResolver(
		func(ctx context.Context, name string) (string, bool) {
			switch name {
			case "server_url":
				return "https://test.satellites", true
			case "version":
				return verb.Version, true
			case "cli_version":
				return verb.CLIVersionEffective(), true
			case "os":
				return verb.OSFromContext(ctx), true
			case "arch":
				return verb.ArchFromContext(ctx), true
			}
			return "", false
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

	// Seed the system_variables doc (boot does this too; test owns its env).
	// Must run after auth truncates so the seed row survives.
	if err := document.SeedSystem(context.Background(), docStore, "system_variables", string(documents.SystemVariablesMarkdown()), "system:seed", time.Now()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	admin, err := authStore.GetUserByEmail(context.Background(), auth.DevAdminEmail)
	if err != nil {
		t.Fatalf("lookup admin: %v", err)
	}
	ctxAdmin := authWithUser(context.Background(), admin)

	t.Run("agent can read the system_variables taxonomy", func(t *testing.T) {
		raw, err := verb.Dispatch(ctxAdmin, "document_get",
			json.RawMessage(`{"name":"system_variables","scope":"system"}`))
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		var got verb.DocumentGetResponse
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"version", "cli_version", "os", "arch", "server_url", "current_version", "state"} {
			if !strings.Contains(got.RenderedBody, "`"+want+"`") {
				t.Errorf("system_variables doc missing %q entry", want)
			}
		}
	})

	t.Run("agent authors a templated workspace doc and it renders", func(t *testing.T) {
		ws, err := wsStore.Create(context.Background(), admin.ID, "dogfood-s8", time.Now())
		if err != nil {
			t.Fatalf("create ws: %v", err)
		}
		if err := wsStore.AddMember(context.Background(), ws.ID, admin.ID, workspace.RoleAdmin, admin.ID, time.Now()); err != nil {
			t.Fatalf("add admin: %v", err)
		}

		// Author the doc using only {{server_url}} from the taxonomy.
		body := "satellites-server lives at {{server_url}}"
		if _, err := verb.Dispatch(ctxAdmin, "document_upsert", json.RawMessage(
			`{"name":"orientation","scope":"workspace","workspace_id":"`+ws.ID+`","body":"`+body+`"}`,
		)); err != nil {
			t.Fatalf("upsert: %v", err)
		}

		raw, err := verb.Dispatch(ctxAdmin, "document_get", json.RawMessage(
			`{"name":"orientation","scope":"workspace","workspace_id":"`+ws.ID+`"}`,
		))
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		var got verb.DocumentGetResponse
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got.RenderedBody != "satellites-server lives at https://test.satellites" {
			t.Fatalf("rendered=%q", got.RenderedBody)
		}
		if len(got.UnresolvedVars) != 0 {
			t.Fatalf("unresolved: %+v", got.UnresolvedVars)
		}
	})
}
