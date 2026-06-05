package workstate

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestOpenCreatesAndMigrates: opening a fresh path creates the file and a usable
// schema — no setup step (AC2).
func TestOpenCreatesAndMigrates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	// A second open of the same file must be a no-op migrate, not an error.
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	s2.Close()
}

// TestAppendAndCurrentProjection: an append upserts the `current` projection and
// Current returns it; a close removes it (AC4).
func TestAppendAndCurrentProjection(t *testing.T) {
	s := openTemp(t)
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	lease := now.Add(time.Hour)

	if _, err := s.Append(Event{Session: "sess1", Story: "sty_a", Phase: "in_progress", Kind: "engage", LeaseUntil: lease, TS: now}); err != nil {
		t.Fatalf("Append engage: %v", err)
	}

	got, ok, err := s.Current("sess1", "sty_a")
	if err != nil || !ok {
		t.Fatalf("Current after engage: ok=%v err=%v", ok, err)
	}
	if got.Phase != "in_progress" {
		t.Fatalf("phase = %q, want in_progress", got.Phase)
	}
	if !got.LeaseUntil.Equal(lease) {
		t.Fatalf("lease = %v, want %v", got.LeaseUntil, lease)
	}

	// Advance the phase — the projection updates in place.
	if _, err := s.Append(Event{Session: "sess1", Story: "sty_a", Phase: "deploy", Kind: "phase", TS: now.Add(time.Minute)}); err != nil {
		t.Fatalf("Append phase: %v", err)
	}
	got, _, _ = s.Current("sess1", "sty_a")
	if got.Phase != "deploy" {
		t.Fatalf("phase after advance = %q, want deploy", got.Phase)
	}

	// Close — the projection row is removed.
	if _, err := s.Append(Event{Session: "sess1", Story: "sty_a", Kind: KindClose, TS: now.Add(2 * time.Minute)}); err != nil {
		t.Fatalf("Append close: %v", err)
	}
	if _, ok, _ := s.Current("sess1", "sty_a"); ok {
		t.Fatalf("Current after close: still present")
	}
}

// TestCurrentIsKeyedPerSessionAndStory: two sessions on the same story, and one
// session on two stories, are independent rows (the multi-story / parallel key).
func TestCurrentIsKeyedPerSessionAndStory(t *testing.T) {
	s := openTemp(t)
	now := time.Now().UTC()
	for _, ev := range []Event{
		{Session: "A", Story: "sty_1", Phase: "in_progress", Kind: "engage", TS: now},
		{Session: "B", Story: "sty_1", Phase: "backlog", Kind: "engage", TS: now},
		{Session: "A", Story: "sty_2", Phase: "in_progress", Kind: "engage", TS: now},
	} {
		if _, err := s.Append(ev); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	all, err := s.ListCurrent()
	if err != nil {
		t.Fatalf("ListCurrent: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("ListCurrent = %d rows, want 3", len(all))
	}
	// Closing A/sty_1 leaves B/sty_1 and A/sty_2 untouched.
	if _, err := s.Append(Event{Session: "A", Story: "sty_1", Kind: KindClose, TS: now}); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, ok, _ := s.Current("A", "sty_1"); ok {
		t.Fatalf("A/sty_1 still open after close")
	}
	if _, ok, _ := s.Current("B", "sty_1"); !ok {
		t.Fatalf("B/sty_1 wrongly removed")
	}
}

// TestConcurrentWritersDoNotCorrupt: parallel appends all land (WAL + busy
// timeout), exercising the parallel-agent path (AC4/AC6).
func TestConcurrentWritersDoNotCorrupt(t *testing.T) {
	s := openTemp(t)
	const n = 50
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.Append(Event{Session: "sess", Story: "sty_x", Phase: "in_progress", Kind: "phase", TS: time.Now().UTC()})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Append: %v", err)
		}
	}
	if c := s.countEvents(t); c != n {
		t.Fatalf("event count = %d, want %d", c, n)
	}
}

func TestAppendRejectsIncomplete(t *testing.T) {
	s := openTemp(t)
	if _, err := s.Append(Event{Story: "sty_a", Kind: "engage"}); err == nil {
		t.Fatalf("expected error for missing session")
	}
	if _, err := s.Append(Event{Session: "s", Kind: "engage"}); err == nil {
		t.Fatalf("expected error for missing story")
	}
	if _, err := s.Append(Event{Session: "s", Story: "sty_a"}); err == nil {
		t.Fatalf("expected error for missing kind")
	}
}

// TestLiveEngagementAndLeaseFresh: LiveEngagement returns a session's open rows;
// a fresh-lease engagement is admitted while a candidate (no lease) or an expired
// one is not — the lookup the edit backstop composes with workflow editability (AC5).
func TestLiveEngagementAndLeaseFresh(t *testing.T) {
	s := openTemp(t)
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	mustAppend(t, s, Event{Session: "sess1", Story: "sty_a", Phase: "in_progress", Kind: "engage", LeaseUntil: now.Add(time.Hour), TS: now})
	mustAppend(t, s, Event{Session: "sess1", Story: "sty_b", Phase: "candidate", Kind: "candidate", TS: now})
	mustAppend(t, s, Event{Session: "sess2", Story: "sty_c", Phase: "in_progress", Kind: "engage", LeaseUntil: now.Add(time.Hour), TS: now})

	engs, err := s.LiveEngagement("sess1")
	if err != nil {
		t.Fatalf("LiveEngagement: %v", err)
	}
	if len(engs) != 2 {
		t.Fatalf("LiveEngagement(sess1) = %d rows, want 2 (sess2 excluded)", len(engs))
	}
	var a, b Engagement
	for _, e := range engs {
		switch e.Story {
		case "sty_a":
			a = e
		case "sty_b":
			b = e
		}
	}
	if !a.IsLeaseFresh(now) {
		t.Errorf("sty_a lease should be fresh at now")
	}
	if a.IsLeaseFresh(now.Add(2 * time.Hour)) {
		t.Errorf("sty_a lease should be expired 2h later")
	}
	if b.IsLeaseFresh(now) {
		t.Errorf("candidate (no lease) must not count as lease-fresh")
	}
}

func mustAppend(t *testing.T, s *Store, ev Event) {
	t.Helper()
	if _, err := s.Append(ev); err != nil {
		t.Fatalf("Append: %v", err)
	}
}

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func (s *Store) countEvents(t *testing.T) int {
	t.Helper()
	var c int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&c); err != nil {
		t.Fatalf("count: %v", err)
	}
	return c
}
