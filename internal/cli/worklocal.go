// Local working-state for the reviewer loop (sty_8e8ec0e7), now consolidated
// onto the per-repo SQLite store (sty_676e070c). The legacy mechanism — a
// per-story .satellites/work/<story>/ tree with an inbox/<seq>-<kind>.json
// append log, status.json, and a .lock flock — is retired: the inbox + claim
// state are store rows (internal/workstate), so .satellites/work/ holds only
// state.db. The server ledger stays the system of record; the store is the
// client-owned cache + message bus.
//
// What remains in package cli is the thin shaping that the layering guard keeps
// out of internal/workstate: rendering a tail of staged inbox messages into the
// ledger_list-shaped response the gate path unmarshals, and the per-operator
// claimant identity.

package cli

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/bobmcallan/satellites/internal/workstate"
)

// localRecentLedgerJSON renders the recent inbox tail as a ledger_list-shaped
// `{"entries":[…]}` document, served from a store query (no directory walk), so
// the caller can unmarshal it into the same response type ledger_list returns —
// recent context with no wire round-trip (AC4). ok is false when the inbox is
// empty, signalling the caller to fall back to ledger_list. The InboxMessage
// JSON mirrors the ledger Entry fields (kind/body/payload/created_at); the extra
// `seq` is ignored on the consumer's unmarshal.
func localRecentLedgerJSON(store *workstate.Store, storyID string, n int) ([]byte, bool, error) {
	msgs, err := store.InboxReadRecent(storyID, n)
	if err != nil {
		return nil, false, err
	}
	if len(msgs) == 0 {
		return nil, false, nil
	}
	b, err := json.Marshal(map[string]any{"entries": msgs})
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

// workClaimant is a stable per-operator/host identity for the lease, so a
// re-run by the same operator reclaims its own lease while a different
// reviewer with a live lease is refused.
func workClaimant(userID string) string {
	host, _ := os.Hostname()
	u := strings.TrimSpace(userID)
	if u == "" {
		u = "local"
	}
	if host == "" {
		host = "host"
	}
	return u + "@" + host
}
