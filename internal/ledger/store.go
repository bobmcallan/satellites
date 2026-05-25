package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotFound is returned when a ledger lookup misses (currently
// only by future direct GetByID callers — List returns an empty
// slice for unknown story ids).
var ErrNotFound = errors.New("ledger: not found")

// Store wraps DB-backed ledger operations against the evidence
// table.
type Store struct {
	DB *sql.DB
}

// New returns a Store bound to the given database/sql handle.
func New(db *sql.DB) *Store { return &Store{DB: db} }

// AppendInput is the typed shape of one ledger insert. StoryID and
// Kind are required; Actor / Body / Payload / Refs are optional.
type AppendInput struct {
	StoryID string
	Kind    string
	Actor   string
	Body    string
	Payload json.RawMessage
	Refs    json.RawMessage
}

// Append inserts one row. The DB rejects future UPDATE / DELETE on
// the resulting row (append-only enforcement at the trigger
// layer); this function therefore never has a corresponding
// counterpart.
func (s *Store) Append(ctx context.Context, in AppendInput, now time.Time) (Entry, error) {
	if strings.TrimSpace(in.StoryID) == "" {
		return Entry{}, fmt.Errorf("ledger: story_id required")
	}
	if strings.TrimSpace(in.Kind) == "" {
		return Entry{}, fmt.Errorf("ledger: kind required")
	}
	now = now.UTC()
	id := NewID()
	payload := in.Payload
	if len(payload) == 0 {
		payload = json.RawMessage("{}")
	}
	refs := in.Refs
	if len(refs) == 0 {
		refs = json.RawMessage("[]")
	}
	if _, err := s.DB.ExecContext(ctx, `
        INSERT INTO evidence
            (id, story_id, kind, body, refs, payload, created_at, created_by)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
    `, id, in.StoryID, in.Kind, in.Body, []byte(refs), []byte(payload), now, in.Actor); err != nil {
		return Entry{}, fmt.Errorf("ledger: append: %w", err)
	}
	return Entry{
		ID:        id,
		StoryID:   in.StoryID,
		Kind:      in.Kind,
		Actor:     in.Actor,
		Body:      in.Body,
		Payload:   payload,
		Refs:      refs,
		CreatedAt: now,
	}, nil
}

// List returns every entry for the given story, oldest-first.
// When kind is non-empty the result is filtered to that kind.
// Returns an empty slice (not nil) when no rows match.
func (s *Store) List(ctx context.Context, storyID, kind string) ([]Entry, error) {
	if strings.TrimSpace(storyID) == "" {
		return nil, fmt.Errorf("ledger: story_id required")
	}
	q := `SELECT id, story_id, kind, COALESCE(created_by,''), COALESCE(body,''),
                 payload, refs, created_at
          FROM evidence
          WHERE story_id = $1`
	args := []any{storyID}
	if strings.TrimSpace(kind) != "" {
		q += ` AND kind = $2`
		args = append(args, kind)
	}
	q += ` ORDER BY created_at ASC, id ASC`
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("ledger: list: %w", err)
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var (
			e             Entry
			payload, refs []byte
		)
		if err := rows.Scan(&e.ID, &e.StoryID, &e.Kind, &e.Actor, &e.Body,
			&payload, &refs, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("ledger: scan: %w", err)
		}
		e.Payload = json.RawMessage(payload)
		e.Refs = json.RawMessage(refs)
		out = append(out, e)
	}
	return out, rows.Err()
}
