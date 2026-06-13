package blob

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestNewID pins the canonical blob id shape.
func TestNewID(t *testing.T) {
	id := NewID()
	if !strings.HasPrefix(id, "blob_") || len(id) != len("blob_")+8 {
		t.Fatalf("NewID = %q, want blob_<8hex>", id)
	}
}

// TestCreate_Validation: required ids and non-empty content are checked BEFORE
// any DB access, so these error paths are unit-testable against a nil-DB store.
func TestCreate_Validation(t *testing.T) {
	s := &Store{} // DB nil — validation must fail before any DB use
	now := time.Unix(0, 0)
	if _, err := s.Create(context.Background(), CreateInput{ProjectID: "p"}, []byte("x"), now); err == nil {
		t.Error("missing workspace_id should error before DB use")
	}
	// project_id is optional (workspace-corpus blob, sty_3c2f02bf): a
	// workspace-only input passes validation and proceeds to DB use — with a
	// nil DB that panics rather than returning the validation error, so we only
	// assert the empty-content guard below still fires before any DB access.
	if _, err := s.Create(context.Background(), CreateInput{WorkspaceID: "w", ProjectID: "p"}, nil, now); err == nil {
		t.Error("empty content should error before DB use")
	}
}
