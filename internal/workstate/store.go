// Package workstate is the per-repo engagement event store (sty_fce7c749,
// epic:process-adherence). It replaces the single engagement.json and
// worklocal.go's hand-rolled seq-files + status.json + flock with one
// SQLite-backed append-only event log plus a materialized `current` projection,
// so the two core questions — what is each agent working on, and is it
// progressing — are answerable as queries.
//
// The store is per-repo, lives at <repo>/.satellites/work/state.db by default
// (resolved via cliconfig.ResolveStateDB; override-only state_db key), and is
// self-initializing: Open creates the file and migrates the schema, so a fresh
// repo needs no setup step. The driver is pure-Go (modernc.org/sqlite) so the
// client stays a no-cgo static binary; WAL mode permits concurrent writers for
// parallel agents.
package workstate

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// KindClose is the event kind that retires a (session, story) engagement from
// the `current` projection. Any other kind upserts/advances it. The lifecycle
// story (sty_8852d23e) defines the richer kind vocabulary (engage/phase/lease);
// the store only needs to know which kind removes a row.
const KindClose = "close"

// Event is one append-only row in the log. Session/Story/Phase locate the
// engagement; Kind is the ledger-style discriminator; Tags travel with the row
// ("log list with tags"); LeaseUntil is optional freshness the door reads.
type Event struct {
	Session    string
	Story      string
	Phase      string
	Kind       string
	Tags       []string
	LeaseUntil time.Time // zero ⇒ no lease recorded on this event
	TS         time.Time // zero ⇒ stamped by Append's caller-supplied now
}

// Engagement is one row of the `current` projection: the latest state for a
// (session, story) pair. It is the single indexed read the access/edit hooks do.
type Engagement struct {
	Session    string
	Story      string
	Phase      string
	LeaseUntil time.Time // zero ⇒ no lease
	UpdatedAt  time.Time
}

// Store is the engagement event store. Safe for concurrent use (database/sql
// pools connections; WAL + busy_timeout serialise writers).
type Store struct {
	db *sql.DB
}

// Open opens the store at path, creating the file and migrating the schema if
// absent (self-initializing — no separate init step). Parent directories are
// created as needed.
func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("workstate: empty store path")
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("workstate: mkdir %s: %w", dir, err)
		}
	}
	// WAL for concurrent readers + a single writer; busy_timeout so a contended
	// write waits rather than erroring under parallel agents.
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("workstate: open %s: %w", path, err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// migrate creates the schema if it does not yet exist. Idempotent: re-opening an
// existing store is a no-op.
func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS events (
    seq         INTEGER PRIMARY KEY AUTOINCREMENT,
    session     TEXT NOT NULL,
    story       TEXT NOT NULL,
    phase       TEXT NOT NULL DEFAULT '',
    kind        TEXT NOT NULL,
    tags        TEXT NOT NULL DEFAULT '[]',
    lease_until TEXT NOT NULL DEFAULT '',
    ts          TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_events_session_story ON events(session, story);

CREATE TABLE IF NOT EXISTS current (
    session     TEXT NOT NULL,
    story       TEXT NOT NULL,
    phase       TEXT NOT NULL DEFAULT '',
    lease_until TEXT NOT NULL DEFAULT '',
    updated_at  TEXT NOT NULL,
    PRIMARY KEY (session, story)
);`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("workstate: migrate: %w", err)
	}
	return nil
}

// Close releases the underlying database handle.
func (s *Store) Close() error { return s.db.Close() }

// Append writes one event and updates the `current` projection: a KindClose
// event removes the (session, story) row; any other kind upserts it (advancing
// phase + lease). Returns the assigned seq. ev.TS defaults to now-UTC when zero.
func (s *Store) Append(ev Event) (int64, error) {
	if strings.TrimSpace(ev.Session) == "" || strings.TrimSpace(ev.Story) == "" {
		return 0, fmt.Errorf("workstate: append needs session and story")
	}
	if strings.TrimSpace(ev.Kind) == "" {
		return 0, fmt.Errorf("workstate: append needs a kind")
	}
	ts := ev.TS
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	tags, err := json.Marshal(nonNil(ev.Tags))
	if err != nil {
		return 0, fmt.Errorf("workstate: encode tags: %w", err)
	}
	lease := rfc3339OrEmpty(ev.LeaseUntil)

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`INSERT INTO events (session, story, phase, kind, tags, lease_until, ts)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ev.Session, ev.Story, ev.Phase, ev.Kind, string(tags), lease, ts.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return 0, fmt.Errorf("workstate: insert event: %w", err)
	}
	seq, _ := res.LastInsertId()

	if ev.Kind == KindClose {
		if _, err := tx.Exec(`DELETE FROM current WHERE session = ? AND story = ?`, ev.Session, ev.Story); err != nil {
			return 0, fmt.Errorf("workstate: close projection: %w", err)
		}
	} else {
		if _, err := tx.Exec(
			`INSERT INTO current (session, story, phase, lease_until, updated_at)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(session, story) DO UPDATE SET
			     phase = excluded.phase,
			     lease_until = excluded.lease_until,
			     updated_at = excluded.updated_at`,
			ev.Session, ev.Story, ev.Phase, lease, ts.UTC().Format(time.RFC3339),
		); err != nil {
			return 0, fmt.Errorf("workstate: upsert projection: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return seq, nil
}

// Current returns the live engagement for (session, story) from the projection —
// the single indexed read the hooks rely on. ok is false when none is open.
func (s *Store) Current(session, story string) (Engagement, bool, error) {
	row := s.db.QueryRow(
		`SELECT session, story, phase, lease_until, updated_at FROM current WHERE session = ? AND story = ?`,
		session, story,
	)
	e, err := scanEngagement(row)
	if err == sql.ErrNoRows {
		return Engagement{}, false, nil
	}
	if err != nil {
		return Engagement{}, false, err
	}
	return e, true, nil
}

// ListCurrent returns every open engagement, newest-updated first — the basis
// for `satellites work status` and the cross-agent view.
func (s *Store) ListCurrent() ([]Engagement, error) {
	rows, err := s.db.Query(
		`SELECT session, story, phase, lease_until, updated_at FROM current ORDER BY updated_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Engagement
	for rows.Next() {
		e, err := scanEngagement(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanEngagement(sc scanner) (Engagement, error) {
	var session, story, phase, lease, updated string
	if err := sc.Scan(&session, &story, &phase, &lease, &updated); err != nil {
		return Engagement{}, err
	}
	return Engagement{
		Session:    session,
		Story:      story,
		Phase:      phase,
		LeaseUntil: parseRFC3339(lease),
		UpdatedAt:  parseRFC3339(updated),
	}, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func rfc3339OrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func parseRFC3339(s string) time.Time {
	if strings.TrimSpace(s) == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
