package cli

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/workstate"
)

// TestRunWorkSync_IdempotentAndOfflineTolerant: events flush once, a second sync
// flushes nothing, and a failed append leaves its event unflushed for retry (AC1/AC5).
func TestRunWorkSync_IdempotentAndOfflineTolerant(t *testing.T) {
	stateDB := filepath.Join(t.TempDir(), "state.db")
	now := time.Now().UTC()

	store, err := workstate.Open(stateDB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(workstate.Event{Session: "s1", Story: "sty_a", Phase: "in_progress", Kind: "engage", TS: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(workstate.Event{Session: "s1", Story: "sty_a", Phase: "deploy", Kind: "phase", TS: now}); err != nil {
		t.Fatal(err)
	}
	store.Close()

	var got []workstate.LoggedEvent
	ok := func(ev workstate.LoggedEvent) error { got = append(got, ev); return nil }

	var out bytes.Buffer
	if err := runWorkSync(&out, stateDB, ok); err != nil {
		t.Fatalf("sync 1: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("first sync flushed %d events, want 2", len(got))
	}
	// Flushed rows carry session_id (AC2) and an engagement kind via the appender.
	if got[0].Session != "s1" {
		t.Errorf("flushed event missing session: %+v", got[0])
	}

	// Second sync: nothing new (idempotent high-water mark).
	got = nil
	if err := runWorkSync(io.Discard, stateDB, ok); err != nil {
		t.Fatalf("sync 2: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("second sync flushed %d, want 0 (idempotent)", len(got))
	}

	// New event + a failing (offline) appender: must NOT advance the mark.
	store, _ = workstate.Open(stateDB)
	if _, err := store.Append(workstate.Event{Session: "s1", Story: "sty_a", Phase: "done", Kind: "phase", TS: now}); err != nil {
		t.Fatal(err)
	}
	store.Close()
	failing := func(ev workstate.LoggedEvent) error { return fmt.Errorf("offline") }
	if err := runWorkSync(io.Discard, stateDB, failing); err != nil {
		t.Fatalf("offline sync should not error (best-effort): %v", err)
	}

	// A later working sync re-flushes the previously-failed event.
	got = nil
	if err := runWorkSync(io.Discard, stateDB, ok); err != nil {
		t.Fatalf("retry sync: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("after offline, retry flushed %d, want 1", len(got))
	}
}
