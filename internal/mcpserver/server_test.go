package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bobmcallan/satellites/internal/verb"
)

// TestServerConstructs verifies New() returns without panic and that
// the underlying verb registry has the built-ins required by the AC.
func TestServerConstructs(t *testing.T) {
	s := New()
	if s == nil {
		t.Fatal("New returned nil")
	}

	catalog := verb.Catalog()
	required := []string{"version", "session_bootstrap", "auth", "verb_discovery"}
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
