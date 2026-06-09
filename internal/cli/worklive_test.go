package cli

import (
	"context"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/workstate"
)

// TestTouchEngagementActivity_throttleAndProjection verifies the START-door
// activity touch: it emits at most once per window (throttle), and it never
// clobbers the engagement's phase/editability projection (re-stamps the same
// values under a fresh lease). The real-time emit is stubbed so no server is
// needed.
func TestTouchEngagementActivity_throttleAndProjection(t *testing.T) {
	root := t.TempDir()
	dbPath := stateDBForRoot(root)

	st, err := workstate.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	base := time.Now().UTC()
	if _, err := st.Append(workstate.Event{
		Session: "s1", Story: "sty_x", Phase: "in_progress", Kind: "engage",
		LeaseUntil: base.Add(time.Hour), Editable: true, TS: base,
	}); err != nil {
		t.Fatalf("seed engage: %v", err)
	}
	st.Close()

	var emits int
	orig := realtimeEmitFn
	realtimeEmitFn = func(ctx context.Context, configArg string) { emits++ }
	defer func() { realtimeEmitFn = orig }()

	eng := workstate.Engagement{Session: "s1", Story: "sty_x", Phase: "in_progress", Editable: true}

	// First touch is due → emits and preserves the projection.
	touchEngagementActivity(root, root, "s1", eng, base.Add(time.Minute))
	if emits != 1 {
		t.Fatalf("first touch: emits=%d, want 1", emits)
	}
	assertProjection(t, dbPath, "s1", "sty_x", "in_progress", true)

	// Second touch within the window is throttled → no emit.
	touchEngagementActivity(root, root, "s1", eng, base.Add(time.Minute+10*time.Second))
	if emits != 1 {
		t.Fatalf("throttle failed: emits=%d, want 1", emits)
	}

	// Past the window → due again.
	touchEngagementActivity(root, root, "s1", eng, base.Add(2*time.Minute))
	if emits != 2 {
		t.Fatalf("post-window touch: emits=%d, want 2", emits)
	}

	// An empty story is a no-op (boundary/ungated allow).
	touchEngagementActivity(root, root, "s1", workstate.Engagement{}, base.Add(3*time.Minute))
	if emits != 2 {
		t.Fatalf("empty-story touch should no-op: emits=%d, want 2", emits)
	}
}

func assertProjection(t *testing.T, dbPath, session, story, wantPhase string, wantEditable bool) {
	t.Helper()
	st, err := workstate.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer st.Close()
	e, ok, err := st.Current(session, story)
	if err != nil || !ok {
		t.Fatalf("current missing: ok=%v err=%v", ok, err)
	}
	if e.Phase != wantPhase || e.Editable != wantEditable {
		t.Fatalf("projection clobbered: phase=%q editable=%v, want %q %v", e.Phase, e.Editable, wantPhase, wantEditable)
	}
}
