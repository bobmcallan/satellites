package changelog

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Store is the DB-backed changelog reader/writer.
type Store struct {
	DB *sql.DB
}

// New returns a Store bound to a database/sql handle.
func New(db *sql.DB) *Store { return &Store{DB: db} }

// CreateInput carries the fields changelog_add accepts. Service +
// version_to + content are required.
type CreateInput struct {
	Service       string
	VersionFrom   string
	VersionTo     string
	Content       string
	CreatedBy     string
	EffectiveDate *time.Time
}

// Create inserts a new entry. Mints id, stamps created_at/updated_at.
func (s *Store) Create(ctx context.Context, in CreateInput, now time.Time) (Entry, error) {
	if strings.TrimSpace(in.Service) == "" {
		return Entry{}, fmt.Errorf("changelog: service required")
	}
	if strings.TrimSpace(in.VersionTo) == "" {
		return Entry{}, fmt.Errorf("changelog: version_to required")
	}
	if strings.TrimSpace(in.Content) == "" {
		return Entry{}, fmt.Errorf("changelog: content required")
	}
	id := NewID()
	now = now.UTC()

	var createdBy sql.NullString
	if in.CreatedBy != "" {
		createdBy = sql.NullString{String: in.CreatedBy, Valid: true}
	}
	var effective sql.NullTime
	if in.EffectiveDate != nil {
		effective = sql.NullTime{Time: in.EffectiveDate.UTC(), Valid: true}
	}

	if _, err := s.DB.ExecContext(ctx, `
        INSERT INTO changelog
            (id, service, version_from, version_to, content,
             created_by, created_at, updated_at, effective_date)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $7, $8)
    `, id, in.Service, in.VersionFrom, in.VersionTo, in.Content,
		createdBy, now, effective); err != nil {
		return Entry{}, fmt.Errorf("changelog: insert: %w", err)
	}

	out := Entry{
		ID:          id,
		Service:     in.Service,
		VersionFrom: in.VersionFrom,
		VersionTo:   in.VersionTo,
		Content:     in.Content,
		CreatedBy:   in.CreatedBy,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if in.EffectiveDate != nil {
		t := in.EffectiveDate.UTC()
		out.EffectiveDate = &t
	}
	return out, nil
}

// GetByID returns the entry at id or ErrNotFound.
func (s *Store) GetByID(ctx context.Context, id string) (Entry, error) {
	row := s.DB.QueryRowContext(ctx, selectColumns+" FROM changelog WHERE id = $1", id)
	e, err := scanCommon(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, ErrNotFound
	}
	if err != nil {
		return Entry{}, fmt.Errorf("changelog: scan: %w", err)
	}
	return e, nil
}

// ListOptions configures List. Service is an optional filter; Cursor +
// Limit drive pagination. The default Limit is 50; the cap is 200.
type ListOptions struct {
	Service string
	Cursor  string
	Limit   int
}

// ListResult carries one page of entries plus the next cursor.
type ListResult struct {
	Items      []Entry
	NextCursor string
}

// List returns a page of entries ordered by
// (effective_date DESC NULLS LAST, created_at DESC, id). Cursor is the
// opaque token from a previous page's NextCursor; empty means first
// page.
func (s *Store) List(ctx context.Context, opts ListOptions) (ListResult, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	var (
		where  []string
		args   []any
		nextIx = 1
	)
	if opts.Service != "" {
		where = append(where, fmt.Sprintf("service = $%d", nextIx))
		args = append(args, opts.Service)
		nextIx++
	}
	if opts.Cursor != "" {
		t, id, err := decodeCursor(opts.Cursor)
		if err != nil {
			return ListResult{}, fmt.Errorf("changelog: %w", err)
		}
		// Cursor pages on created_at (the secondary key — stable and
		// always non-NULL) so a (NULL effective_date, identical
		// created_at) tie is broken by id. Matches the listing index
		// ordering for backfill rows.
		where = append(where,
			fmt.Sprintf("(created_at, id) < ($%d, $%d)", nextIx, nextIx+1))
		args = append(args, t.UTC(), id)
		nextIx += 2
	}

	q := selectColumns + " FROM changelog"
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY effective_date DESC NULLS LAST, created_at DESC, id DESC"
	q += fmt.Sprintf(" LIMIT $%d", nextIx)
	args = append(args, limit+1)

	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return ListResult{}, fmt.Errorf("changelog: list: %w", err)
	}
	defer rows.Close()
	out := make([]Entry, 0, limit)
	for rows.Next() {
		e, err := scanCommon(rows)
		if err != nil {
			return ListResult{}, fmt.Errorf("changelog: scan: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return ListResult{}, err
	}

	var next string
	if len(out) > limit {
		last := out[limit-1]
		out = out[:limit]
		next = encodeCursor(last.CreatedAt, last.ID)
	}
	return ListResult{Items: out, NextCursor: next}, nil
}

// UpdateInput carries the mutable fields changelog_update accepts.
// Each pointer is "set when non-nil"; a non-nil *string with empty
// value clears the column (effective_date set to nil clears the
// timestamp).
type UpdateInput struct {
	Service       *string
	VersionFrom   *string
	VersionTo     *string
	Content       *string
	EffectiveDate **time.Time
}

// Update patches the entry at id. Returns the updated row.
func (s *Store) Update(ctx context.Context, id string, in UpdateInput, now time.Time) (Entry, error) {
	now = now.UTC()
	e, err := s.GetByID(ctx, id)
	if err != nil {
		return Entry{}, err
	}
	if in.Service != nil {
		if strings.TrimSpace(*in.Service) == "" {
			return Entry{}, fmt.Errorf("changelog: service cannot be cleared")
		}
		e.Service = *in.Service
	}
	if in.VersionFrom != nil {
		e.VersionFrom = *in.VersionFrom
	}
	if in.VersionTo != nil {
		if strings.TrimSpace(*in.VersionTo) == "" {
			return Entry{}, fmt.Errorf("changelog: version_to cannot be cleared")
		}
		e.VersionTo = *in.VersionTo
	}
	if in.Content != nil {
		if strings.TrimSpace(*in.Content) == "" {
			return Entry{}, fmt.Errorf("changelog: content cannot be cleared")
		}
		e.Content = *in.Content
	}
	if in.EffectiveDate != nil {
		if *in.EffectiveDate == nil {
			e.EffectiveDate = nil
		} else {
			t := (*in.EffectiveDate).UTC()
			e.EffectiveDate = &t
		}
	}
	e.UpdatedAt = now

	var effective sql.NullTime
	if e.EffectiveDate != nil {
		effective = sql.NullTime{Time: *e.EffectiveDate, Valid: true}
	}
	if _, err := s.DB.ExecContext(ctx, `
        UPDATE changelog
        SET service = $1, version_from = $2, version_to = $3,
            content = $4, effective_date = $5, updated_at = $6
        WHERE id = $7
    `, e.Service, e.VersionFrom, e.VersionTo, e.Content, effective, now, id); err != nil {
		return Entry{}, fmt.Errorf("changelog: update: %w", err)
	}
	return e, nil
}

// Delete hard-deletes the entry at id. Returns ErrNotFound when no
// matching row exists.
func (s *Store) Delete(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx, "DELETE FROM changelog WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("changelog: delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

const selectColumns = `SELECT id, service, version_from, version_to,
    content, created_by, created_at, updated_at, effective_date`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanCommon(s rowScanner) (Entry, error) {
	var (
		e         Entry
		createdBy sql.NullString
		effective sql.NullTime
	)
	if err := s.Scan(&e.ID, &e.Service, &e.VersionFrom, &e.VersionTo,
		&e.Content, &createdBy, &e.CreatedAt, &e.UpdatedAt, &effective); err != nil {
		return Entry{}, err
	}
	if createdBy.Valid {
		e.CreatedBy = createdBy.String
	}
	if effective.Valid {
		t := effective.Time
		e.EffectiveDate = &t
	}
	return e, nil
}

func encodeCursor(t time.Time, id string) string {
	raw := fmt.Sprintf("%s|%s", t.UTC().Format(time.RFC3339Nano), id)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(c string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("invalid cursor encoding")
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", fmt.Errorf("invalid cursor shape")
	}
	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", fmt.Errorf("invalid cursor timestamp")
	}
	return t, parts[1], nil
}
