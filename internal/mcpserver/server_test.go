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

// TestMCPSurfaceIsBootstrapOnly pins the MCP tool surface to the
// bootstrap verbs. document_get returns the install schema; project_match
// resolves a project_id from the consumer repo's git remote. Every
// operational verb is reachable via the satellites CLI once the agent
// has installed it using the orientation instructions. If this test
// starts failing, the MCP server has regrown its surface area — confirm
// intent before relaxing the assertion.
func TestMCPSurfaceIsBootstrapOnly(t *testing.T) {
	s := New()
	tools := s.ListTools()
	want := map[string]bool{"document_get": true, "project_match": true}
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
