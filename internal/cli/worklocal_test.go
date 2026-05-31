package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// chdirTemp switches CWD to a fresh temp dir for the duration of the test, so
// the CWD-relative .satellites/work tree is isolated and auto-cleaned.
func chdirTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	return dir
}

// TestInboxAppend_MonotonicSeqAndOrder pins the append-only message bus: each
// append gets the next seq, files land under inbox/, and readAll returns them
// oldest-first.
func TestInboxAppend_MonotonicSeqAndOrder(t *testing.T) {
	chdirTemp(t)
	now := time.Unix(1700000000, 0).UTC()
	for i, kind := range []string{"review_requested", "step_summary", "log:info"} {
		seq, err := inboxAppend("sty_x", kind, "body "+kind, nil, now.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatalf("append %s: %v", kind, err)
		}
		if seq != i+1 {
			t.Fatalf("append %s seq = %d, want %d", kind, seq, i+1)
		}
	}
	// log:info must have been filename-sanitised.
	if _, err := os.Stat(filepath.Join(inboxDir("sty_x"), "000003-log_info.json")); err != nil {
		t.Errorf("sanitised log:info filename missing: %v", err)
	}
	msgs, err := inboxReadAll("sty_x")
	if err != nil {
		t.Fatalf("readAll: %v", err)
	}
	if len(msgs) != 3 || msgs[0].Seq != 1 || msgs[2].Seq != 3 {
		t.Fatalf("readAll order/seq wrong: %+v", msgs)
	}
	if msgs[0].Kind != "review_requested" || msgs[2].Kind != "log:info" {
		t.Fatalf("kinds not preserved: %+v", msgs)
	}
}

// TestClaimWork_RefusesDoubleClaim pins AC5: a live lease held by a different
// reviewer is refused; an expired lease is reclaimable; the same claimant can
// always re-claim (crash-recovery / iterative re-review).
func TestClaimWork_RefusesDoubleClaim(t *testing.T) {
	chdirTemp(t)
	t0 := time.Unix(1700000000, 0).UTC()
	lease := 10 * time.Minute

	if err := claimWork("sty_y", "alice@h", "in_progress", lease, t0); err != nil {
		t.Fatalf("alice initial claim: %v", err)
	}
	// bob, while alice's lease is live → refused.
	if err := claimWork("sty_y", "bob@h", "in_progress", lease, t0.Add(time.Minute)); !errors.Is(err, errWorkClaimed) {
		t.Fatalf("bob double-claim should be refused, got %v", err)
	}
	// alice re-claims her own live lease → allowed.
	if err := claimWork("sty_y", "alice@h", "in_progress", lease, t0.Add(2*time.Minute)); err != nil {
		t.Fatalf("alice re-claim should be allowed: %v", err)
	}
	// bob after the lease expires → allowed.
	if err := claimWork("sty_y", "bob@h", "in_progress", lease, t0.Add(20*time.Minute)); err != nil {
		t.Fatalf("bob claim after expiry should be allowed: %v", err)
	}
	st, err := readWorkStatus("sty_y")
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	if st.ClaimedBy != "bob@h" {
		t.Fatalf("claimed_by = %q, want bob@h", st.ClaimedBy)
	}
}

// TestLocalRecentLedgerJSON_FallbackAndShape pins AC1: an absent/empty inbox
// signals fallback (ok=false); a populated inbox renders a ledger_list-shaped
// {"entries":[…]} doc whose rows carry the ledger fields.
func TestLocalRecentLedgerJSON_FallbackAndShape(t *testing.T) {
	chdirTemp(t)
	if _, ok, err := localRecentLedgerJSON("sty_z", 5); err != nil || ok {
		t.Fatalf("empty inbox: ok=%v err=%v, want ok=false", ok, err)
	}
	now := time.Unix(1700000000, 0).UTC()
	if _, err := inboxAppend("sty_z", "review_requested", "gate ran", json.RawMessage(`{"gate":"g"}`), now); err != nil {
		t.Fatalf("append: %v", err)
	}
	raw, ok, err := localRecentLedgerJSON("sty_z", 5)
	if err != nil || !ok {
		t.Fatalf("populated inbox: ok=%v err=%v, want ok=true", ok, err)
	}
	var doc struct {
		Entries []struct {
			Kind string `json:"kind"`
			Body string `json:"body"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal entries doc: %v", err)
	}
	if len(doc.Entries) != 1 || doc.Entries[0].Kind != "review_requested" || doc.Entries[0].Body != "gate ran" {
		t.Fatalf("entries shape wrong: %+v", doc.Entries)
	}
}

// TestWorkStatus_LastSeqRoundtrip confirms status.json persists the flush
// high-water mark across reads (the idempotent-flush key).
func TestWorkStatus_LastSeqRoundtrip(t *testing.T) {
	chdirTemp(t)
	if err := writeWorkStatus("sty_w", workStatus{StoryID: "sty_w", LastSeq: 7}); err != nil {
		t.Fatalf("write: %v", err)
	}
	st, err := readWorkStatus("sty_w")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if st.LastSeq != 7 {
		t.Fatalf("LastSeq = %d, want 7", st.LastSeq)
	}
}

// TestCleanupWork removes the whole work area.
func TestCleanupWork(t *testing.T) {
	chdirTemp(t)
	now := time.Unix(1700000000, 0).UTC()
	if _, err := inboxAppend("sty_c", "review_requested", "x", nil, now); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := cleanupWork("sty_c"); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := os.Stat(storyWorkDir("sty_c")); !os.IsNotExist(err) {
		t.Fatalf("work dir should be gone, stat err = %v", err)
	}
}
