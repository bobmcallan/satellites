package workspace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
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

// Update field-merges name and/or description onto the workspace at id and
// bumps updated_at. A nil pointer leaves that column untouched (set-when-
// present, mirroring document/changelog patch semantics); a non-nil empty
// string is an explicit clear. Ownership is never touched here — owner moves
// are out of scope. ErrNotFound when no row matches. With nothing to set it
// still touches updated_at so the caller reads a consistent row back.
func (s *Store) Update(ctx context.Context, id string, name, description *string, now time.Time) (Workspace, error) {
	if id == "" {
		return Workspace{}, fmt.Errorf("workspace: id required")
	}
	now = now.UTC()
	sets := []string{"updated_at = $1"}
	args := []any{now}
	n := 2
	if name != nil {
		if *name == "" {
			return Workspace{}, fmt.Errorf("workspace: name cannot be cleared")
		}
		sets = append(sets, fmt.Sprintf("name = $%d", n))
		args = append(args, *name)
		n++
	}
	if description != nil {
		sets = append(sets, fmt.Sprintf("description = $%d", n))
		args = append(args, *description)
		n++
	}
	args = append(args, id)
	q := "UPDATE workspaces SET " + strings.Join(sets, ", ") + fmt.Sprintf(" WHERE id = $%d", n)
	res, err := s.DB.ExecContext(ctx, q, args...)
	if err != nil {
		return Workspace{}, fmt.Errorf("workspace: update: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return Workspace{}, fmt.Errorf("workspace: update rows: %w", err)
	}
	if rows == 0 {
		return Workspace{}, ErrNotFound
	}
	return s.GetByID(ctx, id)
}

// GetByID returns the workspace with the given id, or ErrNotFound.
func (s *Store) GetByID(ctx context.Context, id string) (Workspace, error) {
	row := s.DB.QueryRowContext(ctx, `
        SELECT id, name, description, owner_user_id, status, is_default, created_at, updated_at, seed_md, seed_updated_at
        FROM workspaces
        WHERE id = $1
    `, id)
	return scanWorkspace(row)
}

// GetDefault returns the workspace flagged is_default, or ErrNotFound.
func (s *Store) GetDefault(ctx context.Context) (Workspace, error) {
	row := s.DB.QueryRowContext(ctx, `
        SELECT id, name, description, owner_user_id, status, is_default, created_at, updated_at, seed_md, seed_updated_at
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
        SELECT id, name, description, owner_user_id, status, is_default, created_at, updated_at, seed_md, seed_updated_at
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

// GetPersonalForUser returns the user's personal workspace (the one
// flagged is_personal that they own), or ErrNotFound. This is the
// deterministic "my workspace" resolver (epic:user-admin, sty_3a54bf42):
// the workspaces_one_personal_per_owner unique index guarantees at most
// one such row per owner.
func (s *Store) GetPersonalForUser(ctx context.Context, userID string) (Workspace, error) {
	if userID == "" {
		return Workspace{}, ErrNotFound
	}
	row := s.DB.QueryRowContext(ctx, `
        SELECT id, name, description, owner_user_id, status, is_default, created_at, updated_at, seed_md, seed_updated_at
        FROM workspaces
        WHERE owner_user_id = $1 AND is_personal = TRUE
        LIMIT 1
    `, userID)
	return scanWorkspace(row)
}

// EnsurePersonalWorkspace returns the user's existing personal workspace,
// or mints one (owner=user, role owner) when none exists. Idempotent —
// re-login does not create duplicates (the unique index would reject a
// second personal row anyway). name seeds a fresh workspace's display
// name; it is ignored when one already exists.
func (s *Store) EnsurePersonalWorkspace(ctx context.Context, userID, name string, now time.Time) (Workspace, error) {
	if userID == "" {
		return Workspace{}, fmt.Errorf("workspace: user_id required")
	}
	if w, err := s.GetPersonalForUser(ctx, userID); err == nil {
		return w, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Workspace{}, err
	}
	if name == "" {
		name = "Personal"
	}
	now = now.UTC()
	id := NewID()

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Workspace{}, fmt.Errorf("workspace: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
        INSERT INTO workspaces (id, name, owner_user_id, status, is_default, is_personal, created_at, updated_at)
        VALUES ($1, $2, $3, $4, FALSE, TRUE, $5, $5)
    `, id, name, userID, StatusActive, now); err != nil {
		return Workspace{}, fmt.Errorf("workspace: insert personal: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO workspace_members (workspace_id, user_id, role, added_at, added_by)
        VALUES ($1, $2, 'owner', $3, $2)
        ON CONFLICT (workspace_id, user_id) DO UPDATE SET role = 'owner'
    `, id, userID, now); err != nil {
		return Workspace{}, fmt.Errorf("workspace: seed personal owner membership: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Workspace{}, fmt.Errorf("workspace: commit: %w", err)
	}
	return Workspace{
		ID:          id,
		Name:        name,
		OwnerUserID: userID,
		Status:      StatusActive,
		IsDefault:   false,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// rowScanner is the common surface of *sql.Row and *sql.Rows we Scan
// against. Avoids duplicating the column list in two helpers.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanWorkspaceCommon(s rowScanner) (Workspace, error) {
	var (
		w         Workspace
		owner     sql.NullString
		seedAtRow sql.NullTime
	)
	if err := s.Scan(&w.ID, &w.Name, &w.Description, &owner, &w.Status, &w.IsDefault, &w.CreatedAt, &w.UpdatedAt, &w.SeedMD, &seedAtRow); err != nil {
		return Workspace{}, err
	}
	if owner.Valid {
		w.OwnerUserID = owner.String
	}
	if seedAtRow.Valid {
		t := seedAtRow.Time
		w.SeedUpdatedAt = &t
	}
	return w, nil
}

// ApplySeed writes body into seed_md and bumps seed_updated_at +
// updated_at on the workspace at id. Idempotent: when body matches the
// stored seed_md byte-for-byte, no write occurs and the second return
// value (changed) is false.
//
// Sole caller is the workspace_seed_apply verb, driven by
// `satellites seed push` walking `.satellites/seeds/<wksp>/workspace.md`.
func (s *Store) ApplySeed(ctx context.Context, id, body string, now time.Time) (Workspace, bool, error) {
	now = now.UTC()
	w, err := s.GetByID(ctx, id)
	if err != nil {
		return Workspace{}, false, err
	}
	if w.SeedMD == body {
		return w, false, nil
	}
	if _, err := s.DB.ExecContext(ctx, `
        UPDATE workspaces
        SET seed_md = $1, seed_updated_at = $2, updated_at = $2
        WHERE id = $3
    `, body, now, id); err != nil {
		return Workspace{}, false, fmt.Errorf("workspace: apply seed: %w", err)
	}
	w.SeedMD = body
	w.SeedUpdatedAt = &now
	w.UpdatedAt = now
	return w, true, nil
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
