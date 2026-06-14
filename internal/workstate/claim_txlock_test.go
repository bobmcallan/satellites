package workstate

import (
	"path/filepath"
	"testing"
	"time"
)

// TestClaimWorkSerialisesAcrossConnections is the regression for sty_3683308d:
// a second connection's ClaimWork must WAIT for a held write lock (under
// busy_timeout) and succeed, rather than returning SQLITE_BUSY immediately on a
// read-to-write lock upgrade. Two Store handles on the same file stand in for
// the two processes a techdebt traverse runs (parent status_transition + the
// spawned techdebt-review child). Before the _txlock=immediate fix, B's deferred
// SELECT-then-UPSERT upgrade errored instantly; with it, B blocks at BEGIN
// IMMEDIATE and proceeds once A releases.
func TestClaimWorkSerialisesAcrossConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	a, err := Open(path)
	if err != nil {
		t.Fatalf("open A: %v", err)
	}
	defer a.Close()
	b, err := Open(path)
	if err != nil {
		t.Fatalf("open B: %v", err)
	}
	defer b.Close()

	// Hold a write transaction open on A so the database's write lock is taken.
	// With _txlock=immediate, Begin acquires the reserved/write lock right here.
	txA, err := a.db.Begin()
	if err != nil {
		t.Fatalf("A begin: %v", err)
	}
	if _, err := txA.Exec(
		`INSERT INTO work_claim (story, status, claimed_by, lease_until, last_seq) VALUES ('sty_hold','held','A','',0)`,
	); err != nil {
		t.Fatalf("A write: %v", err)
	}

	// Concurrently, B claims a different story. Its write transaction must wait
	// for A's lock rather than failing with "database is locked".
	claimErr := make(chan error, 1)
	go func() {
		claimErr <- b.ClaimWork("sty_b", "B", "techdebt-review", time.Hour, time.Now().UTC())
	}()

	// Let B start and block on the lock, then release A.
	time.Sleep(300 * time.Millisecond)
	if err := txA.Commit(); err != nil {
		t.Fatalf("A commit: %v", err)
	}

	select {
	case err := <-claimErr:
		if err != nil {
			t.Fatalf("contended ClaimWork should wait and succeed, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("contended ClaimWork did not return within 5s (lock not released?)")
	}

	// The claim actually landed for B.
	wc, err := b.ReadWorkClaim("sty_b")
	if err != nil {
		t.Fatalf("read claim: %v", err)
	}
	if wc.ClaimedBy != "B" || wc.Status != "techdebt-review" {
		t.Fatalf("claim not recorded as expected: %+v", wc)
	}
}
