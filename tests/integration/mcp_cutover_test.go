//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/config/seed"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/mcpserver"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

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

	if err := document.SeedSystem(context.Background(), docStore, "satellites_client_install", string(seed.ClientInstallMarkdown()), "system:seed", time.Now()); err != nil {
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
		body := string(seed.MCPLoadContextMarkdown())
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
		if !strings.Contains(got.RenderedBody, "satellites-v0.0.21-linux-amd64") {
			t.Fatalf("install URL missing or wrong version segment: %s", got.RenderedBody)
		}
		if !strings.Contains(got.RenderedBody, "target_install_path: ./.satellites/satellites") {
			t.Fatalf("target_install_path missing from rendered schema")
		}
		if len(got.UnresolvedVars) != 0 {
			t.Fatalf("unresolved variables in install schema render: %+v", got.UnresolvedVars)
		}
	})
}
