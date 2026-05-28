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

// AppendInput is the typed shape of one ledger insert. Kind is the
// only required field. Every correlation id (StoryID, ProjectID,
// WorkspaceID, SessionID, RunID) is optional; the arbor LedgerHandler
// path populates them from request context, legacy story-only callers
// populate StoryID alone.
type AppendInput struct {
	StoryID     string
	ProjectID   string
	WorkspaceID string
	SessionID   string
	RunID       string
	Kind        string
	Actor       string
	Body        string
	Payload     json.RawMessage
	Refs        json.RawMessage
}

// ListFilter parameterises List with any subset of correlation ids
// plus an optional kind. Empty fields are not constrained — a filter
// with all fields empty would return every row, so callers must set at
// least one selectable field. The Limit / Cursor pair paginates oldest
// to newest; Cursor is the created_at|id of the last row from the
// previous page.
type ListFilter struct {
	StoryID     string
	ProjectID   string
	WorkspaceID string
	SessionID   string
	RunID       string
	Kind        string
	Limit       int
	Cursor      string
}

// ListResult carries the rows + the next cursor (empty when the page
// reached the end of the matching set).
type ListResult struct {
	Entries    []Entry
	NextCursor string
}

// Append inserts one row. The DB rejects future UPDATE / DELETE on
// the resulting row (append-only enforcement at the trigger
// layer); this function therefore never has a corresponding
// counterpart. Kind is required; all correlation ids are optional and
// stored as empty strings when blank.
func (s *Store) Append(ctx context.Context, in AppendInput, now time.Time) (Entry, error) {
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
	// story_id is NULL-able per migration 0023. Pass nil when empty so
	// the column carries NULL rather than the empty string (the empty
	// string would index distinctly from NULL on a future query).
	var storyArg any
	if strings.TrimSpace(in.StoryID) == "" {
		storyArg = nil
	} else {
		storyArg = in.StoryID
	}
	if _, err := s.DB.ExecContext(ctx, `
        INSERT INTO evidence
            (id, story_id, project_id, workspace_id, session_id, run_id,
             kind, body, refs, payload, created_at, created_by)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
    `, id, storyArg, in.ProjectID, in.WorkspaceID, in.SessionID, in.RunID,
		in.Kind, in.Body, []byte(refs), []byte(payload), now, in.Actor); err != nil {
		return Entry{}, fmt.Errorf("ledger: append: %w", err)
	}
	return Entry{
		ID:          id,
		StoryID:     in.StoryID,
		ProjectID:   in.ProjectID,
		WorkspaceID: in.WorkspaceID,
		SessionID:   in.SessionID,
		RunID:       in.RunID,
		Kind:        in.Kind,
		Actor:       in.Actor,
		Body:        in.Body,
		Payload:     payload,
		Refs:        refs,
		CreatedAt:   now,
	}, nil
}

// AppendMany inserts a batch of rows in a single multi-row INSERT. The
// arbor LedgerHandler's drainer uses this to amortise the round-trip
// across however many events the batch window collected. On any
// error, no rows are written (the batch is atomic at the statement
// level).
func (s *Store) AppendMany(ctx context.Context, ins []AppendInput, now time.Time) error {
	if len(ins) == 0 {
		return nil
	}
	now = now.UTC()
	// 12 columns per row, $1..$N positional placeholders.
	const cols = 12
	args := make([]any, 0, len(ins)*cols)
	values := make([]string, 0, len(ins))
	for i, in := range ins {
		if strings.TrimSpace(in.Kind) == "" {
			return fmt.Errorf("ledger: kind required (index %d)", i)
		}
		base := i*cols + 1
		values = append(values, fmt.Sprintf(
			"($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
			base, base+1, base+2, base+3, base+4, base+5,
			base+6, base+7, base+8, base+9, base+10, base+11))
		payload := in.Payload
		if len(payload) == 0 {
			payload = json.RawMessage("{}")
		}
		refs := in.Refs
		if len(refs) == 0 {
			refs = json.RawMessage("[]")
		}
		var storyArg any
		if strings.TrimSpace(in.StoryID) == "" {
			storyArg = nil
		} else {
			storyArg = in.StoryID
		}
		args = append(args,
			NewID(), storyArg, in.ProjectID, in.WorkspaceID, in.SessionID, in.RunID,
			in.Kind, in.Body, []byte(refs), []byte(payload), now, in.Actor)
	}
	q := `INSERT INTO evidence
        (id, story_id, project_id, workspace_id, session_id, run_id,
         kind, body, refs, payload, created_at, created_by)
        VALUES ` + strings.Join(values, ",")
	if _, err := s.DB.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("ledger: append_many: %w", err)
	}
	return nil
}

// ListByStory is the back-compat shape for story-only callers (the
// reviewer hook, summary regeneration, the request_review gate). It
// returns every entry for the named story, oldest-first, optionally
// filtered to one kind, without pagination. New callers should use
// the richer List filter shape.
func (s *Store) ListByStory(ctx context.Context, storyID, kind string) ([]Entry, error) {
	if strings.TrimSpace(storyID) == "" {
		return nil, fmt.Errorf("ledger: story_id required")
	}
	res, err := s.List(ctx, ListFilter{StoryID: storyID, Kind: kind, Limit: 2000})
	if err != nil {
		return nil, err
	}
	return res.Entries, nil
}

// List returns ledger entries matching the filter, oldest-first.
// At least one of the correlation ids or Kind must be set; an
// unfiltered scan over every row is refused to prevent accidental
// full-table reads from the portal.
//
// Pagination: Limit caps the page; Cursor is the `<created_at>|<id>`
// of the last row in the previous page (empty for the first page).
// NextCursor is empty when the page reaches the tail of the matching
// set.
func (s *Store) List(ctx context.Context, f ListFilter) (ListResult, error) {
	if strings.TrimSpace(f.StoryID) == "" &&
		strings.TrimSpace(f.ProjectID) == "" &&
		strings.TrimSpace(f.WorkspaceID) == "" &&
		strings.TrimSpace(f.SessionID) == "" &&
		strings.TrimSpace(f.RunID) == "" &&
		strings.TrimSpace(f.Kind) == "" {
		return ListResult{}, fmt.Errorf("ledger: at least one filter field required")
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 200
	}
	if limit > 2000 {
		limit = 2000
	}

	conds := []string{}
	args := []any{}
	placeholder := 1
	addCond := func(col, val string) {
		if strings.TrimSpace(val) == "" {
			return
		}
		conds = append(conds, fmt.Sprintf("%s = $%d", col, placeholder))
		args = append(args, val)
		placeholder++
	}
	addCond("story_id", f.StoryID)
	addCond("project_id", f.ProjectID)
	addCond("workspace_id", f.WorkspaceID)
	addCond("session_id", f.SessionID)
	addCond("run_id", f.RunID)
	addCond("kind", f.Kind)

	if strings.TrimSpace(f.Cursor) != "" {
		// Cursor decodes as RFC3339Nano|id; rows past it are the next
		// page. Bad cursors fail loud — there's no partial-decode
		// fallback to mask a coding mistake on the client.
		parts := strings.SplitN(f.Cursor, "|", 2)
		if len(parts) != 2 {
			return ListResult{}, fmt.Errorf("ledger: malformed cursor")
		}
		t, err := time.Parse(time.RFC3339Nano, parts[0])
		if err != nil {
			return ListResult{}, fmt.Errorf("ledger: cursor time: %w", err)
		}
		conds = append(conds, fmt.Sprintf("(created_at, id) > ($%d, $%d)", placeholder, placeholder+1))
		args = append(args, t.UTC(), parts[1])
		placeholder += 2
	}

	q := `SELECT id, COALESCE(story_id,''), COALESCE(project_id,''),
                 COALESCE(workspace_id,''), COALESCE(session_id,''),
                 COALESCE(run_id,''), kind, COALESCE(created_by,''),
                 COALESCE(body,''), payload, refs, created_at
          FROM evidence`
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += fmt.Sprintf(" ORDER BY created_at ASC, id ASC LIMIT %d", limit+1)

	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return ListResult{}, fmt.Errorf("ledger: list: %w", err)
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var (
			e             Entry
			payload, refs []byte
		)
		if err := rows.Scan(&e.ID, &e.StoryID, &e.ProjectID, &e.WorkspaceID,
			&e.SessionID, &e.RunID, &e.Kind, &e.Actor, &e.Body,
			&payload, &refs, &e.CreatedAt); err != nil {
			return ListResult{}, fmt.Errorf("ledger: scan: %w", err)
		}
		e.Payload = json.RawMessage(payload)
		e.Refs = json.RawMessage(refs)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return ListResult{}, err
	}

	res := ListResult{Entries: out}
	// Asking for limit+1 lets us know there's a next page without a
	// second COUNT query: if we got limit+1 rows, trim the last and
	// emit a cursor pointing at the penultimate row's (created_at, id).
	if len(out) > limit {
		res.Entries = out[:limit]
		last := res.Entries[limit-1]
		res.NextCursor = fmt.Sprintf("%s|%s", last.CreatedAt.Format(time.RFC3339Nano), last.ID)
	}
	return res, nil
}
