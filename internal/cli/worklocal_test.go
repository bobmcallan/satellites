package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/workstate"
)

func openTempStore(t *testing.T) *workstate.Store {
	t.Helper()
	s, err := workstate.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestLocalRecentLedgerJSON_FallbackAndShape pins AC4: an empty inbox signals
// fallback (ok=false); a populated inbox renders a ledger_list-shaped
// {"entries":[…]} doc whose rows carry the ledger fields — served from a store
// query, not a directory walk.
func TestLocalRecentLedgerJSON_FallbackAndShape(t *testing.T) {
	store := openTempStore(t)
	if _, ok, err := localRecentLedgerJSON(store, "sty_z", 5); err != nil || ok {
		t.Fatalf("empty inbox: ok=%v err=%v, want ok=false", ok, err)
	}
	now := time.Unix(1700000000, 0).UTC()
	if _, err := store.InboxAppend("sty_z", "review_requested", "gate ran", json.RawMessage(`{"gate":"g"}`), now); err != nil {
		t.Fatalf("append: %v", err)
	}
	raw, ok, err := localRecentLedgerJSON(store, "sty_z", 5)
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

// TestWorkClaimant_Identity confirms the claimant id carries the operator and
// host, and falls back to "local" for an empty user.
func TestWorkClaimant_Identity(t *testing.T) {
	if got := workClaimant("bob"); !strings.HasPrefix(got, "bob@") {
		t.Fatalf("claimant = %q, want bob@<host>", got)
	}
	if got := workClaimant("  "); !strings.HasPrefix(got, "local@") {
		t.Fatalf("empty-user claimant = %q, want local@<host>", got)
	}
}
