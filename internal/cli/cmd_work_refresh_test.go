package cli

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/workstate"
)

// TestRefreshEngagementPhase covers sty_2c232fa4: after a reviewer-enacted
// transition advances the status, refreshing the engagement updates the door's
// editability snapshot in place (no `work init` re-run), and is a no-op when no
// engagement exists for the (session, story).
func TestRefreshEngagementPhase(t *testing.T) {
	store, err := workstate.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 9, 5, 0, 0, 0, time.UTC)
	const session, story = "sess-1", "sty_x"

	// Engage at a non-editable phase (the door would block edits).
	if _, err := store.Append(workstate.Event{
		Session: session, Story: story, Phase: "backlog", Kind: "engage",
		LeaseUntil: now.Add(2 * time.Hour), Editable: false, TS: now,
	}); err != nil {
		t.Fatalf("engage: %v", err)
	}
	if eng, ok, _ := store.Current(session, story); !ok || eng.Editable {
		t.Fatalf("precondition: engagement should be non-editable, got ok=%v editable=%v", ok, eng.Editable)
	}

	// Reviewer advanced backlog→in_progress; refresh to the new editable status.
	later := now.Add(time.Minute)
	refreshed, err := refreshEngagementPhase(store, session, story, "in_progress", true, false, later)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if !refreshed {
		t.Fatal("expected refresh to apply to the live engagement")
	}
	eng, ok, err := store.Current(session, story)
	if err != nil || !ok {
		t.Fatalf("read after refresh: ok=%v err=%v", ok, err)
	}
	if eng.Phase != "in_progress" {
		t.Errorf("phase = %q, want in_progress", eng.Phase)
	}
	if !eng.Editable {
		t.Error("editable = false after refresh to an editable status; the door would still block")
	}
	if !eng.IsLeaseFresh(later) {
		t.Error("lease not fresh after refresh (a zero lease would re-block)")
	}

	// No engagement for the pair → no-op, no fabricated engagement.
	refreshed, err = refreshEngagementPhase(store, "other-sess", story, "in_progress", true, false, later)
	if err != nil {
		t.Fatalf("refresh no-op: %v", err)
	}
	if refreshed {
		t.Error("refresh should be a no-op when no engagement exists")
	}
	if _, ok, _ := store.Current("other-sess", story); ok {
		t.Error("refresh fabricated an engagement for a session that never engaged")
	}
}
