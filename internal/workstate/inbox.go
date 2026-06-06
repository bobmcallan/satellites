// The reviewer-loop's local working state, consolidated onto the store
// (sty_676e070c). Before this, the client kept it as per-story files under
// .satellites/work/<story>/: an inbox/<seq>-<kind>.json append log, a
// status.json claim/lease/last_seq record, and a .lock flock the status
// read-modify-write serialised on. That sprawl is retired here onto two tables
// in the same SQLite store the engagement model already uses, so the whole
// reviewer signal is queryable rows in one place — and .satellites/work/ holds
// only state.db.
//
// Two distinct streams share the store but not the schema: the engagement
// `events`/`current` projection (what each agent is working on) stays as-is;
// this adds the reviewer loop's own `inbox` (messages staged for the server
// ledger) + `work_claim` (the per-story claim/lease + flush high-water mark).
// The flock is gone: claim is a single transaction, serialised by the
// single-writer pool + WAL + busy_timeout (sty_12236a4d), which is the same
// guard the engagement writers rely on.

package workstate

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// InboxMessage is one staged reviewer-loop signal: the ledger fields it carries
// when flushed (kind/body/payload/created_at) plus the store-assigned seq. The
// JSON tags mirror the ledger Entry shape so a tail of these marshals straight
// into a ledger_list-shaped response (localRecentLedgerJSON) with no wire round
// trip; the extra `seq` is ignored by that consumer.
type InboxMessage struct {
	Seq       int64           `json:"seq,omitempty"`
	Kind      string          `json:"kind"`
	Body      string          `json:"body,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	CreatedAt string          `json:"created_at,omitempty"`
}

// WorkClaim is the per-story claim/lease state and the inbox flush high-water
// mark — the row that replaces status.json. A zero value (StoryID == "") means
// no claim row exists yet.
type WorkClaim struct {
	StoryID    string
	Status     string
	ClaimedBy  string
	LeaseUntil string // RFC3339, empty ⇒ none
	LastSeq    int64  // inbox flush high-water mark (flush-once contract)
}

// ErrWorkClaimed is returned by ClaimWork when the work area is held by a
// different reviewer under a still-live lease.
var ErrWorkClaimed = fmt.Errorf("work area already claimed by another reviewer")

// InboxAppend stages one message to the story's inbox and returns its assigned
// seq. seq is the store's global autoincrement rowid — monotonic per story,
// which is all the flush-once high-water mark requires.
func (s *Store) InboxAppend(story, kind, body string, payload json.RawMessage, now time.Time) (int64, error) {
	if strings.TrimSpace(story) == "" || strings.TrimSpace(kind) == "" {
		return 0, fmt.Errorf("workstate: inbox append needs story and kind")
	}
	ts := now
	if ts.IsZero() {
		ts = time.Now()
	}
	res, err := s.db.Exec(
		`INSERT INTO inbox (story, kind, body, payload, created_at) VALUES (?, ?, ?, ?, ?)`,
		story, kind, body, string(payload), ts.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return 0, fmt.Errorf("workstate: inbox append: %w", err)
	}
	return res.LastInsertId()
}

// InboxReadAll returns every staged message for the story, oldest seq first.
func (s *Store) InboxReadAll(story string) ([]InboxMessage, error) {
	return s.queryInbox(`SELECT seq, kind, body, payload, created_at FROM inbox WHERE story = ? ORDER BY seq ASC`, story)
}

// InboxReadRecent returns up to n most-recent staged messages, oldest seq first.
// n <= 0 returns all. Serves the gate's recent-context read from the store
// (replacing the directory walk).
func (s *Store) InboxReadRecent(story string, n int) ([]InboxMessage, error) {
	msgs, err := s.InboxReadAll(story)
	if err != nil {
		return nil, err
	}
	if n > 0 && len(msgs) > n {
		msgs = msgs[len(msgs)-n:]
	}
	return msgs, nil
}

func (s *Store) queryInbox(query string, args ...any) ([]InboxMessage, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InboxMessage
	for rows.Next() {
		var m InboxMessage
		var payload string
		if err := rows.Scan(&m.Seq, &m.Kind, &m.Body, &payload, &m.CreatedAt); err != nil {
			return nil, err
		}
		if strings.TrimSpace(payload) != "" {
			m.Payload = json.RawMessage(payload)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ReadWorkClaim returns the story's claim row, or a zero value (no error) when
// none exists yet.
func (s *Store) ReadWorkClaim(story string) (WorkClaim, error) {
	row := s.db.QueryRow(
		`SELECT story, status, claimed_by, lease_until, last_seq FROM work_claim WHERE story = ?`,
		story,
	)
	var wc WorkClaim
	err := row.Scan(&wc.StoryID, &wc.Status, &wc.ClaimedBy, &wc.LeaseUntil, &wc.LastSeq)
	if err == sql.ErrNoRows {
		return WorkClaim{}, nil
	}
	if err != nil {
		return WorkClaim{}, err
	}
	return wc, nil
}

// ClaimWork acquires the story's work area for claimedBy with a lease, atomic
// under a single store transaction (the WAL + single-writer pool serialise it —
// the flock is gone). It refuses (ErrWorkClaimed) only when an UNEXPIRED lease
// is held by a DIFFERENT claimant, so two reviewers cannot double-claim the same
// transition. An expired lease, or one already held by claimedBy (a re-run /
// crash-recovery by the same operator), is reclaimable. The last_seq flush mark
// is preserved across a re-claim. now is injected for testability.
func (s *Store) ClaimWork(story, claimedBy, status string, lease time.Duration, now time.Time) error {
	if strings.TrimSpace(story) == "" {
		return fmt.Errorf("workstate: claim needs a story")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var existingBy, leaseUntil string
	err = tx.QueryRow(`SELECT claimed_by, lease_until FROM work_claim WHERE story = ?`, story).
		Scan(&existingBy, &leaseUntil)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if existingBy != "" && existingBy != claimedBy && leaseUntil != "" {
		if until := parseRFC3339(leaseUntil); !until.IsZero() && now.Before(until) {
			return fmt.Errorf("%w: held by %q until %s", ErrWorkClaimed, existingBy, leaseUntil)
		}
	}
	newLease := now.Add(lease).UTC().Format(time.RFC3339)
	// COALESCE preserves the existing last_seq high-water mark on a re-claim; a
	// first claim seeds it at 0. The ON CONFLICT update never touches last_seq.
	if _, err := tx.Exec(
		`INSERT INTO work_claim (story, status, claimed_by, lease_until, last_seq)
		 VALUES (?, ?, ?, ?, COALESCE((SELECT last_seq FROM work_claim WHERE story = ?), 0))
		 ON CONFLICT(story) DO UPDATE SET
		     status = excluded.status,
		     claimed_by = excluded.claimed_by,
		     lease_until = excluded.lease_until`,
		story, status, claimedBy, newLease, story,
	); err != nil {
		return fmt.Errorf("workstate: claim upsert: %w", err)
	}
	return tx.Commit()
}

// AdvanceInboxFlush raises the story's flush high-water mark to seq (never
// lowers it), creating the claim row if absent. This is the inbox's flush-once
// contract — the same high-water-mark mechanism `work sync` uses for engagement
// events, here scoped per story.
func (s *Store) AdvanceInboxFlush(story string, seq int64) error {
	if strings.TrimSpace(story) == "" {
		return fmt.Errorf("workstate: advance flush needs a story")
	}
	_, err := s.db.Exec(
		`INSERT INTO work_claim (story, last_seq) VALUES (?, ?)
		 ON CONFLICT(story) DO UPDATE SET last_seq = MAX(work_claim.last_seq, excluded.last_seq)`,
		story, seq,
	)
	if err != nil {
		return fmt.Errorf("workstate: advance flush mark: %w", err)
	}
	return nil
}

// CleanupWork removes the story's inbox + claim rows after its terminal flush,
// so a completed story leaves no working-state residue in the store.
func (s *Store) CleanupWork(story string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM inbox WHERE story = ?`, story); err != nil {
		return fmt.Errorf("workstate: cleanup inbox: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM work_claim WHERE story = ?`, story); err != nil {
		return fmt.Errorf("workstate: cleanup claim: %w", err)
	}
	return tx.Commit()
}
