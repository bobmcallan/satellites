package ledger

import (
	"strings"
	"testing"
)

func TestNewID_Format(t *testing.T) {
	id := NewID()
	if !strings.HasPrefix(id, "evt_") || len(id) != 12 {
		t.Fatalf("malformed id: %q", id)
	}
}
