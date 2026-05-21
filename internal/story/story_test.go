package story

import (
	"strings"
	"testing"
)

func TestNewID_Format(t *testing.T) {
	id := NewID()
	if !strings.HasPrefix(id, "sty_") || len(id) != 12 {
		t.Fatalf("malformed id: %q", id)
	}
}
