package workspace

import (
	"strings"
	"testing"
)

func TestNewID_FormatAndUniqueness(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 100; i++ {
		id := NewID()
		if !strings.HasPrefix(id, "wksp_") {
			t.Fatalf("missing wksp_ prefix: %s", id)
		}
		// "wksp_" + 8 hex chars
		if len(id) != 13 {
			t.Fatalf("unexpected id length %d (want 13): %s", len(id), id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate id minted at i=%d: %s", i, id)
		}
		seen[id] = struct{}{}
	}
}
