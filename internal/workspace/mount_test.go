package workspace

import (
	"context"
	"testing"
	"time"
)

// TestAddMount_Validation: required ids are checked before any DB access, so
// these error paths are unit-testable against a nil-DB store.
func TestAddMount_Validation(t *testing.T) {
	s := &Store{} // DB nil — validation must fail before any DB use
	now := time.Unix(0, 0)
	if err := s.AddMount(context.Background(), "", "p", "", now); err == nil {
		t.Error("missing workspace_id should error before DB use")
	}
	if err := s.AddMount(context.Background(), "w", "", "", now); err == nil {
		t.Error("missing project_id should error before DB use")
	}
}

// TestCallerHasMountAccess_EmptyArgs: empty ids short-circuit to (false, nil)
// before any DB use, so the authz resolver never queries on a blank caller.
func TestCallerHasMountAccess_EmptyArgs(t *testing.T) {
	s := &Store{} // DB nil — empty args short-circuit before DB use
	if ok, err := s.CallerHasMountAccess(context.Background(), "", "u"); err != nil || ok {
		t.Errorf("empty project_id: got (%v,%v), want (false,nil)", ok, err)
	}
	if ok, err := s.CallerHasMountAccess(context.Background(), "p", ""); err != nil || ok {
		t.Errorf("empty user_id: got (%v,%v), want (false,nil)", ok, err)
	}
}
