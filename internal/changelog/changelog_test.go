package changelog

import (
	"strings"
	"testing"
)

func TestNewID_Format(t *testing.T) {
	id := NewID()
	if !strings.HasPrefix(id, "cl_") || len(id) != 11 {
		t.Fatalf("malformed id: %q (want cl_<8hex>)", id)
	}
}
