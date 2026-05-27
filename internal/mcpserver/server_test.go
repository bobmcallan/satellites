package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bobmcallan/satellites/internal/verb"
)

// TestServerConstructs verifies New() returns without panic and that
// the underlying verb registry has the built-ins required by the AC.
// These verbs reach agents via the satellites CLI, not MCP.
func TestServerConstructs(t *testing.T) {
	s := New()
	if s == nil {
		t.Fatal("New returned nil")
	}

	catalog := verb.Catalog()
	required := []string{"version", "document_get", "document_upsert", "variable_get"}
	have := map[string]bool{}
	for _, n := range catalog {
		have[n] = true
	}
	for _, r := range required {
		if !have[r] {
			t.Errorf("registry missing required verb %q (have %v)", r, catalog)
		}
	}
}

// TestMCPSurfaceIsExpected pins the MCP tool surface to the exact set
// the substrate is willing to expose. Four cohorts:
//   - Bootstrap (CLI-installable agents): document_get, project_match
//   - Document CRUD (MCP-only and CLI): document_get, document_list,
//     document_upsert, document_delete
//   - Project CRUD (MCP-only registration + maintenance):
//     project_create, project_list, project_get, project_update,
//     project_match
//   - API-key minting (in-band auth for MCP-only agents):
//     apikey_create, apikey_list, apikey_revoke
//
// If this test fails, the MCP server has grown or shrunk its surface —
// confirm intent and update both this test and exposedVerbs in server.go.
func TestMCPSurfaceIsExpected(t *testing.T) {
	s := New()
	tools := s.ListTools()
	want := map[string]bool{
		"document_get":     true,
		"document_list":    true,
		"document_count":   true,
		"document_upsert":  true,
		"document_delete":  true,
		"project_match":    true,
		"project_create":   true,
		"project_list":     true,
		"project_get":      true,
		"project_update":   true,
		"apikey_create":    true,
		"apikey_list":      true,
		"apikey_revoke":    true,
		"changelog_add":    true,
		"changelog_list":   true,
		"changelog_update": true,
		"changelog_delete": true,
	}
	if len(tools) != len(want) {
		names := make([]string, 0, len(tools))
		for n := range tools {
			names = append(names, n)
		}
		t.Fatalf("MCP surface should expose exactly %d tools, got %d: %v", len(want), len(tools), names)
	}
	for name := range want {
		if _, ok := tools[name]; !ok {
			t.Errorf("MCP surface missing %q; tools = %v", name, tools)
		}
	}
}

// TestExposedToolsMarshalJSON makes sure every exposed tool serialises
// over the wire. mcp.Tool.MarshalJSON refuses tools that have both
// InputSchema and RawInputSchema populated — NewTool seeds
// InputSchema.Type="object" by default, so any ToolOption that sets
// RawInputSchema must also clear InputSchema.Type. Caught in
// production: prod tools/list returned 500 with
// `tool document_delete has both InputSchema and RawInputSchema set`.
func TestExposedToolsMarshalJSON(t *testing.T) {
	s := New()
	tools := s.ListTools()
	for name, st := range tools {
		if _, err := json.Marshal(st.Tool); err != nil {
			t.Errorf("tool %q failed to marshal: %v", name, err)
		}
	}
}

// TestTagsSchemaIsArrayOfStrings pins the input schema for tags on the
// two verbs that accept it (document_upsert, document_list). The bug
// behind this contract: without an inputSchema, hosted MCP clients
// (Claude web) stringified array values, which the dispatcher then
// rejected on unmarshal. A typed schema fixes both ends — the client
// sees array<string> and the server's struct keeps decoding correctly.
func TestTagsSchemaIsArrayOfStrings(t *testing.T) {
	s := New()
	tools := s.ListTools()
	for _, name := range []string{"document_upsert", "document_list"} {
		st, ok := tools[name]
		if !ok {
			t.Fatalf("tool %q missing from MCP surface", name)
		}
		if len(st.Tool.RawInputSchema) == 0 {
			t.Fatalf("tool %q has no RawInputSchema — typed schema lost", name)
		}
		var schema map[string]any
		if err := json.Unmarshal(st.Tool.RawInputSchema, &schema); err != nil {
			t.Fatalf("tool %q: schema unmarshal: %v", name, err)
		}
		props, _ := schema["properties"].(map[string]any)
		tagsField, _ := props["tags"].(map[string]any)
		if tagsField == nil {
			t.Fatalf("tool %q: properties.tags not present; schema=%s", name, st.Tool.RawInputSchema)
		}
		if got := tagsField["type"]; got != "array" {
			t.Errorf("tool %q: properties.tags.type = %v, want \"array\"", name, got)
		}
		items, _ := tagsField["items"].(map[string]any)
		if items == nil {
			t.Fatalf("tool %q: properties.tags.items missing; schema=%s", name, st.Tool.RawInputSchema)
		}
		if got := items["type"]; got != "string" {
			t.Errorf("tool %q: properties.tags.items.type = %v, want \"string\"", name, got)
		}
	}
}

// TestParity_VerbVsRegistry confirms the single-execution-path discipline:
// dispatching `version` through verb.Dispatch (the MCP path) returns the
// same bytes as the registry's direct invoke (the CLI path). Both
// transports share this code path, so the assertion is by construction
// — but the test keeps it that way.
func TestParity_VerbVsRegistry(t *testing.T) {
	ctx := context.Background()

	via, err := verb.Dispatch(ctx, "version", nil)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	direct, err := verb.Get("version").Invoke(ctx, nil)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}

	if string(via) != string(direct) {
		t.Fatalf("parity mismatch:\nvia=%s\ndirect=%s", via, direct)
	}

	// And it round-trips as VersionInfo.
	var info verb.VersionInfo
	if err := json.Unmarshal(via, &info); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if info.Version == "" {
		t.Fatal("empty version")
	}
}
