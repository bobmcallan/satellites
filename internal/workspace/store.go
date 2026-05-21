package workspace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned when a workspace lookup misses.
var ErrNotFound = errors.New("workspace: not found")

// Store wraps DB-backed workspace operations against the workspaces
// table (migration 0005).
type Store struct {
	DB *sql.DB
}

// New returns a Store bound to the given database/sql handle.
func New(db *sql.DB) *Store { return &Store{DB: db} }

// Create inserts a new workspace. ownerUserID may be empty for the
// single-tenant CLI-local case. When non-empty, the creator is
// auto-promoted to admin via a workspace_members row in the same
// transaction — the substrate guarantee is "every workspace has at
// least its creator as admin".
func (s *Store) Create(ctx context.Context, ownerUserID, name string, now time.Time) (Workspace, error) {
	if name == "" {
		return Workspace{}, fmt.Errorf("workspace: name required")
	}
	id := NewID()
	var owner sql.NullString
	if ownerUserID != "" {
		owner = sql.NullString{String: ownerUserID, Valid: true}
	}
	now = now.UTC()

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Workspace{}, fmt.Errorf("workspace: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
        INSERT INTO workspaces (id, name, owner_user_id, status, is_default, created_at, updated_at)
        VALUES ($1, $2, $3, $4, FALSE, $5, $5)
    `, id, name, owner, StatusActive, now); err != nil {
		return Workspace{}, fmt.Errorf("workspace: insert: %w", err)
	}
	if ownerUserID != "" {
		if _, err := tx.ExecContext(ctx, `
            INSERT INTO workspace_members (workspace_id, user_id, role, added_at, added_by)
            VALUES ($1, $2, 'admin', $3, $2)
            ON CONFLICT DO NOTHING
        `, id, ownerUserID, now); err != nil {
			return Workspace{}, fmt.Errorf("workspace: seed creator membership: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Workspace{}, fmt.Errorf("workspace: commit: %w", err)
	}
	return Workspace{
		ID:          id,
		Name:        name,
		OwnerUserID: ownerUserID,
		Status:      StatusActive,
		IsDefault:   false,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// GetByID returns the workspace with the given id, or ErrNotFound.
func (s *Store) GetByID(ctx context.Context, id string) (Workspace, error) {
	row := s.DB.QueryRowContext(ctx, `
        SELECT id, name, owner_user_id, status, is_default, created_at, updated_at
        FROM workspaces
        WHERE id = $1
    `, id)
	return scanWorkspace(row)
}

// GetDefault returns the workspace flagged is_default, or ErrNotFound.
func (s *Store) GetDefault(ctx context.Context) (Workspace, error) {
	row := s.DB.QueryRowContext(ctx, `
        SELECT id, name, owner_user_id, status, is_default, created_at, updated_at
        FROM workspaces
        WHERE is_default = TRUE
        LIMIT 1
    `)
	return scanWorkspace(row)
}

// List returns every workspace, newest-first by created_at. Per-owner
// filtering arrives with the membership PR.
func (s *Store) List(ctx context.Context) ([]Workspace, error) {
	rows, err := s.DB.QueryContext(ctx, `
        SELECT id, name, owner_user_id, status, is_default, created_at, updated_at
        FROM workspaces
        ORDER BY created_at DESC, id
    `)
	if err != nil {
		return nil, fmt.Errorf("workspace: list: %w", err)
	}
	defer rows.Close()
	var out []Workspace
	for rows.Next() {
		w, err := scanRowsWorkspace(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// SetDefault flips is_default on id to TRUE and FALSE on every other
// row, in a single transaction. The partial unique index on
// (is_default) WHERE is_default = TRUE means we must clear the previous
// default BEFORE setting the new one — otherwise the second UPDATE hits
// a uniqueness violation.
func (s *Store) SetDefault(ctx context.Context, id string, now time.Time) (Workspace, error) {
	now = now.UTC()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Workspace{}, fmt.Errorf("workspace: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
        UPDATE workspaces
        SET is_default = FALSE, updated_at = $1
        WHERE is_default = TRUE AND id <> $2
    `, now, id)
	if err != nil {
		return Workspace{}, fmt.Errorf("workspace: clear default: %w", err)
	}
	_ = res

	res, err = tx.ExecContext(ctx, `
        UPDATE workspaces
        SET is_default = TRUE, updated_at = $1
        WHERE id = $2
    `, now, id)
	if err != nil {
		return Workspace{}, fmt.Errorf("workspace: set default: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Workspace{}, fmt.Errorf("workspace: rows affected: %w", err)
	}
	if n == 0 {
		return Workspace{}, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return Workspace{}, fmt.Errorf("workspace: commit: %w", err)
	}
	return s.GetByID(ctx, id)
}

// rowScanner is the common surface of *sql.Row and *sql.Rows we Scan
// against. Avoids duplicating the column list in two helpers.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanWorkspaceCommon(s rowScanner) (Workspace, error) {
	var (
		w     Workspace
		owner sql.NullString
	)
	if err := s.Scan(&w.ID, &w.Name, &owner, &w.Status, &w.IsDefault, &w.CreatedAt, &w.UpdatedAt); err != nil {
		return Workspace{}, err
	}
	if owner.Valid {
		w.OwnerUserID = owner.String
	}
	return w, nil
}

func scanWorkspace(row *sql.Row) (Workspace, error) {
	w, err := scanWorkspaceCommon(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Workspace{}, ErrNotFound
	}
	if err != nil {
		return Workspace{}, fmt.Errorf("workspace: scan: %w", err)
	}
	return w, nil
}

func scanRowsWorkspace(rows *sql.Rows) (Workspace, error) {
	w, err := scanWorkspaceCommon(rows)
	if err != nil {
		return Workspace{}, fmt.Errorf("workspace: scan: %w", err)
	}
	return w, nil
}
