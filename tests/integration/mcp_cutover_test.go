//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/config/documents"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/frontmatter"
	"github.com/bobmcallan/satellites/internal/mcpserver"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// installSchemaSpaces collapses runs of spaces so the install-schema
// assertions match a `key: value` pair regardless of YAML column
// alignment. sty_a62ba0c7 aligned the schema values (e.g.
// `target_install_path:` gained padding spaces); the assertions check
// the logical pair, not its spacing.
var installSchemaSpaces = regexp.MustCompile(" +")

// TestMCPCutover exercises the S7 dogfood: a fresh agent reading only
// the MCP orientation instructions plus document_get should be able to
// reach a working install URL. We can't drive a real MCP client from a
// Go test, so we exercise the equivalent dispatch path the agent
// follows: list MCP tools (single one, document_get), call it with the
// arguments the load context tells the agent to send, parse the
// rendered install schema, and verify the URLs would resolve.
func TestMCPCutover(t *testing.T) {
	env := testbootstrap.SetUp(t)

	docStore := document.New(env.DB)
	verb.SetDocumentStore(docStore)
	t.Cleanup(func() {
		verb.SetDocumentStore(nil)
		verb.SetSystemVariableResolver(nil, nil)
	})

	if err := document.SeedSystem(context.Background(), docStore, "satellites_client_install", string(documents.ClientInstallMarkdown()), "system:seed", time.Now()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	systemVars := map[string]string{
		"version":         "vTest-server",
		"cli_version":     "v0.0.21",
		"server_url":      "https://satellites.example",
		"current_version": "",
		"state":           "install_required",
	}
	verb.SetSystemVariableResolver(
		func(ctx context.Context, name string) (string, bool) {
			switch name {
			case "os":
				return verb.OSFromContext(ctx), true
			case "arch":
				return verb.ArchFromContext(ctx), true
			}
			v, ok := systemVars[name]
			return v, ok
		},
		func(context.Context) []string {
			return []string{"version", "cli_version", "os", "arch", "server_url", "current_version", "state"}
		},
	)

	t.Run("MCP surface exposes the bootstrap + write verbs", func(t *testing.T) {
		s := mcpserver.New()
		tools := s.ListTools()
		want := []string{
			"document_get", "document_list", "document_count", "document_upsert", "document_delete",
			"project_match", "project_create", "project_list", "project_get", "project_update",
			"apikey_create", "apikey_list", "apikey_revoke",
			"changelog_add", "changelog_list", "changelog_update", "changelog_delete",
			"semantic_search", "workspace_objective_generate",
			"system_status",
		}
		if len(tools) != len(want) {
			t.Fatalf("expected %d tools, got %d: %v", len(want), len(tools), tools)
		}
		for _, name := range want {
			if _, ok := tools[name]; !ok {
				t.Fatalf("missing %q; tools=%v", name, tools)
			}
		}
		if _, ok := tools["satellites_init"]; ok {
			t.Fatalf("satellites_init must not be exposed after cutover")
		}
	})

	t.Run("orientation instructions reference the bootstrap verbs", func(t *testing.T) {
		body := string(documents.MCPLoadContextMarkdown())
		for _, want := range []string{"document_get", "project_match"} {
			if !strings.Contains(body, want) {
				t.Fatalf("load context missing %q", want)
			}
		}
		if strings.Contains(body, "satellites_init") {
			t.Fatalf("load context still references satellites_init")
		}
	})

	t.Run("agent's document_get call returns a usable install schema", func(t *testing.T) {
		raw, err := verb.Dispatch(context.Background(), "document_get", json.RawMessage(
			`{"name":"satellites_client_install","scope":"system","os":"linux","arch":"amd64","current_version":""}`,
		))
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		var got verb.DocumentGetResponse
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		norm := installSchemaSpaces.ReplaceAllString(got.RenderedBody, " ")
		if !strings.Contains(norm, "satellites-v0.0.21-linux-amd64") {
			t.Fatalf("install URL missing or wrong version segment: %s", got.RenderedBody)
		}
		if !strings.Contains(norm, "target_install_path: ./.satellites/satellites") {
			t.Fatalf("target_install_path missing from rendered schema")
		}
		if len(got.UnresolvedVars) != 0 {
			t.Fatalf("unresolved variables in install schema render: %+v", got.UnresolvedVars)
		}
	})

	// Regression guard for sty_8da63e77: the production boot path
	// (cmd/satellites-server/main.go) seeds documents with frontmatter
	// stripped — only the body bytes after `---` reach the store. If
	// the install schema lives in frontmatter, the agent reads a body
	// with no machine-readable schema and the bootstrap silently falls
	// back to host-repo grep. Mirror that strip here and assert the
	// schema is still recoverable from the body alone.
	t.Run("schema survives the production frontmatter-strip path", func(t *testing.T) {
		raw := documents.ClientInstallMarkdown()
		fm, body, err := frontmatter.Parse(raw)
		if err != nil {
			t.Fatalf("frontmatter parse: %v", err)
		}
		if fm.Name != "satellites_client_install" || fm.Scope != "system" {
			t.Fatalf("frontmatter identity wrong: name=%q scope=%q", fm.Name, fm.Scope)
		}
		// Re-seed with body only (production behaviour) so the next
		// dispatch reads the same row a freshly-booted server would.
		if err := document.SeedSystem(context.Background(), docStore, "satellites_client_install", string(body), "system:seed", time.Now()); err != nil {
			t.Fatalf("seed body-only: %v", err)
		}
		dispatched, err := verb.Dispatch(context.Background(), "document_get", json.RawMessage(
			`{"name":"satellites_client_install","scope":"system","os":"linux","arch":"amd64","current_version":""}`,
		))
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		var got verb.DocumentGetResponse
		if err := json.Unmarshal(dispatched, &got); err != nil {
			t.Fatal(err)
		}
		norm := installSchemaSpaces.ReplaceAllString(got.RenderedBody, " ")
		for _, want := range []string{
			"target_install_path: ./.satellites/satellites",
			"target_config_path: ./.satellites/satellites.toml",
			"satellites-v0.0.21-linux-amd64",
			"satellites-v0.0.21-linux-amd64.sha256",
			"verb: apikey_create",
		} {
			if !strings.Contains(norm, want) {
				t.Fatalf("rendered body missing %q after frontmatter strip:\n%s", want, got.RenderedBody)
			}
		}
		if len(got.UnresolvedVars) != 0 {
			t.Fatalf("unresolved variables after strip: %+v", got.UnresolvedVars)
		}
	})
}
