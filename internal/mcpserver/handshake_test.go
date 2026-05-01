// sty_e1ab884d — the MCP server's handshake instructions block must
// be sourced from the seeded agent-process artifact when available,
// falling back to HandshakeFallbackInstructions when the resolver
// returns empty. These tests pin both branches.
package mcpserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/agentprocess"
	"github.com/bobmcallan/satellites/internal/document"
)

func TestResolveHandshakeInstructions_FallsBackWhenEmpty(t *testing.T) {
	t.Parallel()
	got := resolveHandshakeInstructions(nil)
	if got != HandshakeFallbackInstructions {
		t.Errorf("nil docs handshake = %q, want fallback %q", got, HandshakeFallbackInstructions)
	}

	emptyStore := document.NewMemoryStore()
	got = resolveHandshakeInstructions(emptyStore)
	if got != HandshakeFallbackInstructions {
		t.Errorf("empty store handshake = %q, want fallback %q", got, HandshakeFallbackInstructions)
	}
}

func TestResolveHandshakeInstructions_ServesSeededBody(t *testing.T) {
	t.Parallel()
	store := document.NewMemoryStore()
	if err := agentprocess.SeedSystemDefault(context.Background(), store, "wksp_a", time.Now().UTC()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got := resolveHandshakeInstructions(store)
	if got == HandshakeFallbackInstructions {
		t.Errorf("seeded handshake fell through to fallback")
	}
	// Pin the fundamentals tokens so the test fails loudly if the
	// resolver ever serves a stripped body.
	for _, want := range []string{"configuration over code", "satellites_project_set", "satellites_story_get"} {
		if !strings.Contains(got, want) {
			t.Errorf("handshake body missing %q", want)
		}
	}
}
