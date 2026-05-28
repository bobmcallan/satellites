package correlation

import (
	"context"
	"testing"
)

func TestWithIDsAndFromContext(t *testing.T) {
	ctx := context.Background()

	if _, ok := FromContext(ctx); ok {
		t.Fatal("unstamped context: expected ok=false")
	}

	ids := IDs{
		RunID:       "run_abc",
		SessionID:   "sess_xyz",
		StoryID:     "sty_1",
		ProjectID:   "proj_2",
		WorkspaceID: "wksp_3",
	}
	ctx = WithIDs(ctx, ids)

	got, ok := FromContext(ctx)
	if !ok {
		t.Fatal("stamped context: expected ok=true")
	}
	if got != ids {
		t.Fatalf("round-trip mismatch: got=%+v want=%+v", got, ids)
	}
}

func TestEmpty(t *testing.T) {
	if !(IDs{}).Empty() {
		t.Fatal("zero IDs should be Empty")
	}
	if (IDs{RunID: "run_1"}).Empty() {
		t.Fatal("with run_id should not be Empty")
	}
	if (IDs{WorkspaceID: "wksp_1"}).Empty() {
		t.Fatal("with workspace_id should not be Empty")
	}
}
