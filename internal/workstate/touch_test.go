package workstate

import (
	"path/filepath"
	"testing"
	"time"
)

func TestActivityTouchDue(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	base := time.Now().UTC()
	window := 45 * time.Second

	// First call (no prior touch) is due and records the touch.
	if !s.ActivityTouchDue("s1", "sty_x", base, window) {
		t.Fatal("first touch should be due")
	}
	// Within the window → not due.
	if s.ActivityTouchDue("s1", "sty_x", base.Add(20*time.Second), window) {
		t.Fatal("touch within window should be throttled")
	}
	// Past the window → due again.
	if !s.ActivityTouchDue("s1", "sty_x", base.Add(50*time.Second), window) {
		t.Fatal("touch past window should be due")
	}
	// A different (session, story) is tracked independently.
	if !s.ActivityTouchDue("s2", "sty_x", base.Add(21*time.Second), window) {
		t.Fatal("distinct session should be due independently")
	}
}
