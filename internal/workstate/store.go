// Package workstate is the per-repo engagement event store (sty_fce7c749,
// epic:process-adherence). It replaces the single engagement.json and
// worklocal.go's hand-rolled seq-files + status.json + flock with one
// SQLite-backed append-only event log plus a materialized `current` projection,
// so the two core questions — what is each agent working on, and is it
// progressing — are answerable as queries.
//
// The store is per-repo, lives at <repo>/.satellites/state.db by default
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
	"strconv"
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
	Session     string
	Story       string
	Phase       string
	Kind        string
	Tags        []string
	LeaseUntil  time.Time // zero ⇒ no lease recorded on this event
	Editable    bool      // whether edits are permitted in this phase (workflow-derived at engage; the door reads it)
	CommitReady bool      // whether this phase IS the workflow's commit-push step (the commit door reads it)
	TS          time.Time // zero ⇒ stamped by Append's caller-supplied now
}

// Engagement is one row of the `current` projection: the latest state for a
// (session, story) pair. It is the single indexed read the access/edit hooks do.
type Engagement struct {
	Session     string
	Story       string
	Phase       string
	LeaseUntil  time.Time // zero ⇒ no lease
	Editable    bool      // edits permitted in this phase (the door requires it)
	CommitReady bool      // this phase IS the workflow's commit-push step (the commit door requires it)
	UpdatedAt   time.Time
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
	// write waits rather than erroring under parallel agents. _txlock=immediate
	// makes every write transaction take its lock at BEGIN (sty_3683308d): a
	// deferred read-then-write txn (e.g. ClaimWork's SELECT-then-UPSERT) that
	// upgrades a read snapshot to a write lock while ANOTHER process has written
	// gets SQLITE_BUSY *immediately* — busy_timeout does not cover that upgrade.
	// BEGIN IMMEDIATE acquires the write lock up front, where busy_timeout DOES
	// apply, so cross-process writers serialise (wait) instead of erroring.
	// Read-only queries stay deferred and concurrent under WAL.
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("workstate: open %s: %w", path, err)
	}
	// SQLite permits exactly one writer. database/sql pools connections, so
	// concurrent writers would each grab a connection and race the single-writer
	// lock — under load that exhausts busy_timeout and surfaces SQLITE_BUSY
	// instead of serialising (sty_12236a4d). Cap the pool to one connection so
	// all access funnels through a single writer; WAL + busy_timeout above keep
	// readers concurrent and give contention headroom. The store is a low-volume
	// per-repo file, so single-connection access is not a throughput concern.
	db.SetMaxOpenConns(1)
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
    editable    INTEGER NOT NULL DEFAULT 1,
    commit_ready INTEGER NOT NULL DEFAULT 0,
    updated_at  TEXT NOT NULL,
    PRIMARY KEY (session, story)
);

CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- The reviewer loop's local signal, consolidated off per-story files
-- (sty_676e070c). inbox is the append log staged for the server ledger;
-- work_claim is the per-story claim/lease + the inbox flush high-water mark.
CREATE TABLE IF NOT EXISTS inbox (
    seq        INTEGER PRIMARY KEY AUTOINCREMENT,
    story      TEXT NOT NULL,
    kind       TEXT NOT NULL,
    body       TEXT NOT NULL DEFAULT '',
    payload    TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_inbox_story ON inbox(story, seq);

CREATE TABLE IF NOT EXISTS work_claim (
    story       TEXT PRIMARY KEY,
    status      TEXT NOT NULL DEFAULT '',
    claimed_by  TEXT NOT NULL DEFAULT '',
    lease_until TEXT NOT NULL DEFAULT '',
    last_seq    INTEGER NOT NULL DEFAULT 0
);

-- Durable QA-evidence trail per story (sty_7d2e9847): gate runs + CI outcomes.
-- Unlike inbox/work_claim (transient, dropped on terminal flush), evidence is
-- the durable record the background audit (order:9) + QA view read out of band.
CREATE TABLE IF NOT EXISTS qa_evidence (
    seq         INTEGER PRIMARY KEY AUTOINCREMENT,
    story       TEXT NOT NULL,
    kind        TEXT NOT NULL,
    label       TEXT NOT NULL DEFAULT '',
    decision    TEXT NOT NULL DEFAULT '',
    notes       TEXT NOT NULL DEFAULT '',
    from_status TEXT NOT NULL DEFAULT '',
    to_status   TEXT NOT NULL DEFAULT '',
    ref         TEXT NOT NULL DEFAULT '',
    ts          TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_qa_evidence_story ON qa_evidence(story, seq);

-- Delivered-context budget time series (sty_e3e8f3ce): the always-on
-- executor-context size tracked over time, with exactly one row flagged the
-- baseline the ratchet compares against.
CREATE TABLE IF NOT EXISTS context_budget (
    seq         INTEGER PRIMARY KEY AUTOINCREMENT,
    ts          TEXT NOT NULL,
    bytes       INTEGER NOT NULL,
    tokens      INTEGER NOT NULL,
    is_baseline INTEGER NOT NULL DEFAULT 0,
    components  TEXT NOT NULL DEFAULT ''
);

-- In-flight reviewer gate / work-step marker per story (sty_8eb57090). A gate
-- run (status_transition) sets a row before its slow claude -p dispatch and
-- clears it on return, so a SEPARATE process — the stopcheck Stop hook, or
-- the work status view — can tell "a prescribed gate is running for this story"
-- from "engaged but idle", instead of misreading a slow gate as an abandoned
-- goal. One row per story (latest gate wins); freshness is judged by the reader
-- against started_at, so a crashed gate's stale marker cannot trap.
CREATE TABLE IF NOT EXISTS active_gate (
    story      TEXT PRIMARY KEY,
    gate       TEXT NOT NULL,
    started_at TEXT NOT NULL
);`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("workstate: migrate: %w", err)
	}
	// Backfill the editable column on stores created before sty_2b6cd041. A
	// duplicate-column error means it's already present — ignore it (idempotent).
	_, _ = s.db.Exec(`ALTER TABLE current ADD COLUMN editable INTEGER NOT NULL DEFAULT 1`)
	// Backfill commit_ready on stores created before sty_e925ff09 (additive,
	// same idempotent pattern). Legacy rows default to NOT commit-ready (0) — the
	// commit door fails closed, so an un-refreshed legacy engagement gates commit.
	_, _ = s.db.Exec(`ALTER TABLE current ADD COLUMN commit_ready INTEGER NOT NULL DEFAULT 0`)
	return nil
}

// Close releases the underlying database handle.
func (s *Store) Close() error { return s.db.Close() }

// ActiveGate is the in-flight reviewer gate / work-step for one story: which
// gate is running and when it started. The reader judges freshness against
// StartedAt (a crashed gate leaves a stale marker, never a permanent one).
type ActiveGate struct {
	Story     string
	Gate      string
	StartedAt time.Time
}

// SetActiveGate records that a gate/work-step is running for a story (latest
// gate wins). Called by status_transition just before its slow dispatch; the
// matching ClearActiveGate runs on return. Cross-process visible: each write is
// its own committed transaction, so the stopcheck in another process sees it.
func (s *Store) SetActiveGate(story, gate string, now time.Time) error {
	if strings.TrimSpace(story) == "" {
		return fmt.Errorf("workstate: active gate needs a story")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	_, err := s.db.Exec(
		`INSERT INTO active_gate(story, gate, started_at) VALUES(?,?,?)
		 ON CONFLICT(story) DO UPDATE SET gate=excluded.gate, started_at=excluded.started_at`,
		story, gate, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("workstate: set active gate: %w", err)
	}
	return nil
}

// ClearActiveGate removes a story's in-flight gate marker. Idempotent — a clear
// with no row is a no-op (a crashed gate that never cleared is handled by the
// reader's freshness check, not here).
func (s *Store) ClearActiveGate(story string) error {
	if _, err := s.db.Exec(`DELETE FROM active_gate WHERE story=?`, story); err != nil {
		return fmt.Errorf("workstate: clear active gate: %w", err)
	}
	return nil
}

// GetActiveGate returns the in-flight gate marker for a story, if any. The
// caller decides whether StartedAt is recent enough to trust (a stale marker
// from a crashed gate must not be read as a live gate).
func (s *Store) GetActiveGate(story string) (ActiveGate, bool, error) {
	var gate, started string
	err := s.db.QueryRow(`SELECT gate, started_at FROM active_gate WHERE story=?`, story).Scan(&gate, &started)
	if err == sql.ErrNoRows {
		return ActiveGate{}, false, nil
	}
	if err != nil {
		return ActiveGate{}, false, fmt.Errorf("workstate: get active gate: %w", err)
	}
	ts, _ := time.Parse(time.RFC3339Nano, started)
	return ActiveGate{Story: story, Gate: gate, StartedAt: ts}, true, nil
}

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
			`INSERT INTO current (session, story, phase, lease_until, editable, commit_ready, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(session, story) DO UPDATE SET
			     phase = excluded.phase,
			     lease_until = excluded.lease_until,
			     editable = excluded.editable,
			     commit_ready = excluded.commit_ready,
			     updated_at = excluded.updated_at`,
			ev.Session, ev.Story, ev.Phase, lease, boolToInt(ev.Editable), boolToInt(ev.CommitReady), ts.UTC().Format(time.RFC3339),
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
		`SELECT session, story, phase, lease_until, editable, commit_ready, updated_at FROM current WHERE session = ? AND story = ?`,
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
	return s.queryCurrent(`SELECT session, story, phase, lease_until, editable, commit_ready, updated_at FROM current ORDER BY updated_at DESC`)
}

// LiveEngagement returns the open engagements for one session, newest first.
// The caller composes lease freshness (Engagement.IsLeaseFresh) and workflow
// editability to decide whether edits are permitted — that policy lives at the
// door (sty_2b6cd041), not in the store.
func (s *Store) LiveEngagement(session string) ([]Engagement, error) {
	return s.queryCurrent(
		`SELECT session, story, phase, lease_until, editable, commit_ready, updated_at FROM current WHERE session = ? ORDER BY updated_at DESC`,
		session,
	)
}

func (s *Store) queryCurrent(query string, args ...any) ([]Engagement, error) {
	rows, err := s.db.Query(query, args...)
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

// LoggedEvent is an event as stored, including its assigned seq — the unit the
// server-sync flush reads and high-water-marks against.
type LoggedEvent struct {
	Seq int64
	Event
}

// EventsSince returns every event with seq greater than `seq`, oldest first —
// the unflushed tail for the server-sync flush.
func (s *Store) EventsSince(seq int64) ([]LoggedEvent, error) {
	rows, err := s.db.Query(
		`SELECT seq, session, story, phase, kind, tags, lease_until, ts FROM events WHERE seq > ? ORDER BY seq ASC`,
		seq,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LoggedEvent
	for rows.Next() {
		var le LoggedEvent
		var tags, lease, ts string
		if err := rows.Scan(&le.Seq, &le.Session, &le.Story, &le.Phase, &le.Kind, &tags, &lease, &ts); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(tags), &le.Tags)
		le.LeaseUntil = parseRFC3339(lease)
		le.TS = parseRFC3339(ts)
		out = append(out, le)
	}
	return out, rows.Err()
}

// LastFlushedSeq returns the high-water mark of events already flushed to the
// server ledger, or 0 when nothing has flushed.
func (s *Store) LastFlushedSeq() (int64, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = 'last_flushed_seq'`).Scan(&v)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	return n, nil
}

// SetLastFlushedSeq advances the flush high-water mark.
func (s *Store) SetLastFlushedSeq(seq int64) error {
	_, err := s.db.Exec(
		`INSERT INTO meta (key, value) VALUES ('last_flushed_seq', ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		strconv.FormatInt(seq, 10),
	)
	return err
}

// IsLeaseFresh reports whether the engagement's lease is still in the future at
// now. A missing lease (zero) is treated as NOT fresh — a proper engagement
// always carries a lease, so the absence is the stale/leftover case the door
// must fail closed on (the VIRE bug). Candidate rows carry no lease and are
// excluded here too; the door additionally excludes them by editable phase.
func (e Engagement) IsLeaseFresh(now time.Time) bool {
	if e.LeaseUntil.IsZero() {
		return false
	}
	return now.Before(e.LeaseUntil)
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanEngagement(sc scanner) (Engagement, error) {
	var session, story, phase, lease, updated string
	var editable, commitReady int
	if err := sc.Scan(&session, &story, &phase, &lease, &editable, &commitReady, &updated); err != nil {
		return Engagement{}, err
	}
	return Engagement{
		Session:     session,
		Story:       story,
		Phase:       phase,
		LeaseUntil:  parseRFC3339(lease),
		Editable:    editable != 0,
		CommitReady: commitReady != 0,
		UpdatedAt:   parseRFC3339(updated),
	}, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
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
