package workstate

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestInboxAppendReadOrder pins the append log: each append gets a monotonic
// seq and readAll returns oldest-first with fields preserved (AC6).
func TestInboxAppendReadOrder(t *testing.T) {
	s := openTemp(t)
	now := time.Unix(1700000000, 0).UTC()
	for i, kind := range []string{"review_requested", "step_summary", "log:info"} {
		seq, err := s.InboxAppend("sty_x", kind, "body "+kind, nil, now.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatalf("append %s: %v", kind, err)
		}
		if seq != int64(i+1) {
			t.Fatalf("append %s seq = %d, want %d", kind, seq, i+1)
		}
	}
	msgs, err := s.InboxReadAll("sty_x")
	if err != nil {
		t.Fatalf("readAll: %v", err)
	}
	if len(msgs) != 3 || msgs[0].Seq != 1 || msgs[2].Seq != 3 {
		t.Fatalf("readAll order/seq wrong: %+v", msgs)
	}
	if msgs[0].Kind != "review_requested" || msgs[2].Kind != "log:info" {
		t.Fatalf("kinds not preserved: %+v", msgs)
	}
	// payload round-trips as raw JSON.
	if _, err := s.InboxAppend("sty_p", "review_requested", "x", json.RawMessage(`{"gate":"g"}`), now); err != nil {
		t.Fatalf("append with payload: %v", err)
	}
	p, _ := s.InboxReadAll("sty_p")
	if len(p) != 1 || string(p[0].Payload) != `{"gate":"g"}` {
		t.Fatalf("payload not preserved: %+v", p)
	}
	// reads are per-story isolated.
	if got, _ := s.InboxReadAll("sty_other"); len(got) != 0 {
		t.Fatalf("sty_other inbox should be empty, got %d", len(got))
	}
}

// TestInboxReadRecentTail confirms ReadRecent returns the n-newest, oldest-first.
func TestInboxReadRecentTail(t *testing.T) {
	s := openTemp(t)
	now := time.Unix(1700000000, 0).UTC()
	for i := 0; i < 7; i++ {
		if _, err := s.InboxAppend("sty_r", "k", "b", nil, now); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	msgs, err := s.InboxReadRecent("sty_r", 5)
	if err != nil {
		t.Fatalf("readRecent: %v", err)
	}
	if len(msgs) != 5 || msgs[0].Seq != 3 || msgs[4].Seq != 7 {
		t.Fatalf("recent tail wrong: %+v", msgs)
	}
}

// TestFlushOnceHighWaterMark pins AC2: AdvanceInboxFlush is the flush-once
// mark — only seqs above it are unflushed, and it never moves backward.
func TestFlushOnceHighWaterMark(t *testing.T) {
	s := openTemp(t)
	now := time.Unix(1700000000, 0).UTC()
	for i := 0; i < 3; i++ {
		if _, err := s.InboxAppend("sty_f", "k", "b", nil, now); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	// Nothing flushed yet.
	claim, _ := s.ReadWorkClaim("sty_f")
	if claim.LastSeq != 0 {
		t.Fatalf("initial last_seq = %d, want 0", claim.LastSeq)
	}
	// Flush through seq 2.
	if err := s.AdvanceInboxFlush("sty_f", 2); err != nil {
		t.Fatalf("advance: %v", err)
	}
	claim, _ = s.ReadWorkClaim("sty_f")
	if claim.LastSeq != 2 {
		t.Fatalf("last_seq = %d, want 2", claim.LastSeq)
	}
	// A lower advance must not move the mark backward (idempotent flush).
	if err := s.AdvanceInboxFlush("sty_f", 1); err != nil {
		t.Fatalf("advance lower: %v", err)
	}
	claim, _ = s.ReadWorkClaim("sty_f")
	if claim.LastSeq != 2 {
		t.Fatalf("last_seq regressed to %d, want 2", claim.LastSeq)
	}
	// Only seq 3 is unflushed.
	msgs, _ := s.InboxReadAll("sty_f")
	unflushed := 0
	for _, m := range msgs {
		if m.Seq > claim.LastSeq {
			unflushed++
		}
	}
	if unflushed != 1 {
		t.Fatalf("unflushed = %d, want 1", unflushed)
	}
}

// TestClaimRefusesDoubleClaim pins AC3: a live lease held by a different
// reviewer is refused; an expired lease is reclaimable; the same claimant can
// always re-claim. The last_seq flush mark survives a re-claim.
func TestClaimRefusesDoubleClaim(t *testing.T) {
	s := openTemp(t)
	t0 := time.Unix(1700000000, 0).UTC()
	lease := 10 * time.Minute

	if err := s.ClaimWork("sty_y", "alice@h", "in_progress", lease, t0); err != nil {
		t.Fatalf("alice initial claim: %v", err)
	}
	// Seed a flush mark, then confirm a re-claim preserves it.
	if err := s.AdvanceInboxFlush("sty_y", 5); err != nil {
		t.Fatalf("advance: %v", err)
	}
	// bob, while alice's lease is live → refused.
	if err := s.ClaimWork("sty_y", "bob@h", "in_progress", lease, t0.Add(time.Minute)); !errors.Is(err, ErrWorkClaimed) {
		t.Fatalf("bob double-claim should be refused, got %v", err)
	}
	// alice re-claims her own live lease → allowed, mark preserved.
	if err := s.ClaimWork("sty_y", "alice@h", "in_progress", lease, t0.Add(2*time.Minute)); err != nil {
		t.Fatalf("alice re-claim should be allowed: %v", err)
	}
	if claim, _ := s.ReadWorkClaim("sty_y"); claim.LastSeq != 5 {
		t.Fatalf("re-claim lost flush mark: last_seq = %d, want 5", claim.LastSeq)
	}
	// bob after the lease expires → allowed.
	if err := s.ClaimWork("sty_y", "bob@h", "in_progress", lease, t0.Add(20*time.Minute)); err != nil {
		t.Fatalf("bob claim after expiry should be allowed: %v", err)
	}
	if claim, _ := s.ReadWorkClaim("sty_y"); claim.ClaimedBy != "bob@h" {
		t.Fatalf("claimed_by = %q, want bob@h", claim.ClaimedBy)
	}
}

// TestConcurrentClaimNoDoubleClaim replaces the flock test (AC3): many
// reviewers race to claim under WAL; with live leases exactly one wins and the
// store is not corrupted.
func TestConcurrentClaimNoDoubleClaim(t *testing.T) {
	s := openTemp(t)
	now := time.Unix(1700000000, 0).UTC()
	const n = 25
	var wg sync.WaitGroup
	results := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			claimant := "rev" + string(rune('a'+i)) + "@h"
			results <- s.ClaimWork("sty_race", claimant, "in_progress", 10*time.Minute, now)
		}(i)
	}
	wg.Wait()
	close(results)
	won, refused, other := 0, 0, 0
	for err := range results {
		switch {
		case err == nil:
			won++
		case errors.Is(err, ErrWorkClaimed):
			refused++
		default:
			other++
		}
	}
	if other != 0 {
		t.Fatalf("unexpected errors: %d (store contention should serialise, not error)", other)
	}
	if won != 1 {
		t.Fatalf("winners = %d, want exactly 1 (no double-claim)", won)
	}
	if refused != n-1 {
		t.Fatalf("refused = %d, want %d", refused, n-1)
	}
}

// TestCleanupWork removes a story's inbox + claim rows (AC5).
func TestCleanupWork(t *testing.T) {
	s := openTemp(t)
	now := time.Unix(1700000000, 0).UTC()
	if _, err := s.InboxAppend("sty_c", "review_requested", "x", nil, now); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.ClaimWork("sty_c", "a@h", "done", time.Minute, now); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := s.CleanupWork("sty_c"); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if msgs, _ := s.InboxReadAll("sty_c"); len(msgs) != 0 {
		t.Fatalf("inbox should be empty after cleanup, got %d", len(msgs))
	}
	if claim, _ := s.ReadWorkClaim("sty_c"); claim.StoryID != "" {
		t.Fatalf("claim should be gone after cleanup, got %+v", claim)
	}
}
