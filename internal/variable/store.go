package variable

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Store wraps DB-backed variable operations.
type Store struct {
	DB *sql.DB
}

// New returns a Store bound to the given database/sql handle.
func New(db *sql.DB) *Store { return &Store{DB: db} }

// SetInput is the write payload for Set. Upsert semantics: replaces
// the value (and secret flag) in place (variables aren't versioned).
type SetInput struct {
	Key    Key
	Value  string
	Secret bool
}

// Set upserts a (scope, workspace_id, project_id, name) row to the
// given value. Returns the resulting Variable row. Accepts every scope
// the table admits (system, workspace, project); the verb layer is
// responsible for refusing system names owned by the computed resolver.
func (s *Store) Set(ctx context.Context, in SetInput, now time.Time) (Variable, error) {
	if err := in.Key.Validate(); err != nil {
		return Variable{}, err
	}
	now = now.UTC()
	v, err := s.Get(ctx, in.Key)
	switch {
	case err == nil:
		if _, err := s.DB.ExecContext(ctx, `
            UPDATE variables SET value = $1, secret = $2, updated_at = $3 WHERE id = $4
        `, in.Value, in.Secret, now, v.ID); err != nil {
			return Variable{}, fmt.Errorf("variable: update: %w", err)
		}
		v.Value = in.Value
		v.Secret = in.Secret
		v.UpdatedAt = now
		return v, nil
	case errors.Is(err, ErrNotFound):
		id := NewID()
		wsArg := nullStr(in.Key.WorkspaceID)
		pjArg := nullStr(in.Key.ProjectID)
		userArg := nullStr(in.Key.UserID)
		if _, err := s.DB.ExecContext(ctx, `
            INSERT INTO variables (id, scope, workspace_id, project_id, user_id, name, value, secret, created_at, updated_at)
            VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)
        `, id, string(in.Key.Scope), wsArg, pjArg, userArg, in.Key.Name, in.Value, in.Secret, now); err != nil {
			return Variable{}, fmt.Errorf("variable: insert: %w", err)
		}
		return Variable{
			ID:          id,
			Scope:       in.Key.Scope,
			WorkspaceID: in.Key.WorkspaceID,
			ProjectID:   in.Key.ProjectID,
			UserID:      in.Key.UserID,
			Name:        in.Key.Name,
			Value:       in.Value,
			Secret:      in.Secret,
			CreatedAt:   now,
			UpdatedAt:   now,
		}, nil
	default:
		return Variable{}, err
	}
}

// SeedSystem inserts a (system, name) row only if it does not already
// exist. Returns created=true when a fresh row was written. Used at
// server boot to install operator-tunable defaults idempotently:
// re-running the seed on a database with operator-set values must not
// overwrite them.
func (s *Store) SeedSystem(ctx context.Context, name, defaultValue string, now time.Time) (created bool, err error) {
	if name == "" {
		return false, fmt.Errorf("variable: name required")
	}
	now = now.UTC()
	id := NewID()
	res, err := s.DB.ExecContext(ctx, `
        INSERT INTO variables (id, scope, workspace_id, project_id, name, value, created_at, updated_at)
        VALUES ($1, 'system', NULL, NULL, $2, $3, $4, $4)
        ON CONFLICT (name) WHERE scope = 'system' DO NOTHING
    `, id, name, defaultValue, now)
	if err != nil {
		return false, fmt.Errorf("variable: seed system: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("variable: seed system rows: %w", err)
	}
	return n == 1, nil
}

// Get returns the Variable for the given Key, or ErrNotFound. Stored
// system rows resolve here; the computed-system resolver lives at the
// verb layer and is consulted only when this returns ErrNotFound.
func (s *Store) Get(ctx context.Context, key Key) (Variable, error) {
	if err := key.Validate(); err != nil {
		return Variable{}, err
	}
	wsArg := nullStr(key.WorkspaceID)
	pjArg := nullStr(key.ProjectID)
	userArg := nullStr(key.UserID)
	row := s.DB.QueryRowContext(ctx, `
        SELECT id, scope, COALESCE(workspace_id,''), COALESCE(project_id,''), COALESCE(user_id,''), name, value, secret, created_at, updated_at
        FROM variables
        WHERE scope = $1
          AND workspace_id IS NOT DISTINCT FROM $2
          AND project_id IS NOT DISTINCT FROM $3
          AND user_id IS NOT DISTINCT FROM $4
          AND name = $5
    `, string(key.Scope), wsArg, pjArg, userArg, key.Name)
	return scanVariableRow(row)
}

// Delete removes the row at Key. ErrNotFound when no row matches.
// Accepts every scope; system deletes drop the stored row only — the
// computed resolver still terminates the cascade.
func (s *Store) Delete(ctx context.Context, key Key) error {
	if err := key.Validate(); err != nil {
		return err
	}
	wsArg := nullStr(key.WorkspaceID)
	pjArg := nullStr(key.ProjectID)
	userArg := nullStr(key.UserID)
	res, err := s.DB.ExecContext(ctx, `
        DELETE FROM variables
        WHERE scope = $1
          AND workspace_id IS NOT DISTINCT FROM $2
          AND project_id IS NOT DISTINCT FROM $3
          AND user_id IS NOT DISTINCT FROM $4
          AND name = $5
    `, string(key.Scope), wsArg, pjArg, userArg, key.Name)
	if err != nil {
		return fmt.Errorf("variable: delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("variable: delete rows: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListByScope returns every stored variable at the given scope.
// For scope=project, both workspace_id and project_id filter; for
// scope=workspace, only workspace_id (project_id IS NULL).
func (s *Store) ListByScope(ctx context.Context, scope Scope, workspaceID, projectID string) ([]Variable, error) {
	switch scope {
	case ScopeWorkspace:
		if workspaceID == "" {
			return nil, fmt.Errorf("variable: workspace_id required")
		}
		rows, err := s.DB.QueryContext(ctx, `
            SELECT id, scope, COALESCE(workspace_id,''), COALESCE(project_id,''), COALESCE(user_id,''), name, value, secret, created_at, updated_at
            FROM variables WHERE scope = 'workspace' AND workspace_id = $1
            ORDER BY name
        `, workspaceID)
		if err != nil {
			return nil, fmt.Errorf("variable: list workspace: %w", err)
		}
		defer rows.Close()
		return collectRows(rows)
	case ScopeProject:
		if workspaceID == "" || projectID == "" {
			return nil, fmt.Errorf("variable: workspace_id+project_id required")
		}
		rows, err := s.DB.QueryContext(ctx, `
            SELECT id, scope, COALESCE(workspace_id,''), COALESCE(project_id,''), COALESCE(user_id,''), name, value, secret, created_at, updated_at
            FROM variables WHERE scope = 'project' AND workspace_id = $1 AND project_id = $2
            ORDER BY name
        `, workspaceID, projectID)
		if err != nil {
			return nil, fmt.Errorf("variable: list project: %w", err)
		}
		defer rows.Close()
		return collectRows(rows)
	case ScopeSystem:
		// Stored-system rows live in the table; the computed-system
		// resolver lives at the verb layer and is folded in there.
		rows, err := s.DB.QueryContext(ctx, `
            SELECT id, scope, COALESCE(workspace_id,''), COALESCE(project_id,''), COALESCE(user_id,''), name, value, secret, created_at, updated_at
            FROM variables WHERE scope = 'system'
            ORDER BY name
        `)
		if err != nil {
			return nil, fmt.Errorf("variable: list system: %w", err)
		}
		defer rows.Close()
		return collectRows(rows)
	default:
		return nil, fmt.Errorf("variable: unknown scope %q", scope)
	}
}

// ListByUser returns every user-scope variable for the given user_id. Kept
// separate from ListByScope, whose signature is (scope, workspaceID,
// projectID) — the user scope keys on user_id, not those columns.
func (s *Store) ListByUser(ctx context.Context, userID string) ([]Variable, error) {
	if userID == "" {
		return nil, fmt.Errorf("variable: user_id required")
	}
	rows, err := s.DB.QueryContext(ctx, `
        SELECT id, scope, COALESCE(workspace_id,''), COALESCE(project_id,''), COALESCE(user_id,''), name, value, secret, created_at, updated_at
        FROM variables WHERE scope = 'user' AND user_id = $1
        ORDER BY name
    `, userID)
	if err != nil {
		return nil, fmt.Errorf("variable: list user: %w", err)
	}
	defer rows.Close()
	return collectRows(rows)
}

func collectRows(rows *sql.Rows) ([]Variable, error) {
	out := []Variable{}
	for rows.Next() {
		v, err := scanVariableRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanVariableCommon(rs rowScanner) (Variable, error) {
	var (
		v      Variable
		scopeS string
	)
	if err := rs.Scan(&v.ID, &scopeS, &v.WorkspaceID, &v.ProjectID, &v.UserID,
		&v.Name, &v.Value, &v.Secret, &v.CreatedAt, &v.UpdatedAt); err != nil {
		return Variable{}, err
	}
	v.Scope = Scope(scopeS)
	return v, nil
}

func scanVariableRow(row *sql.Row) (Variable, error) {
	v, err := scanVariableCommon(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Variable{}, ErrNotFound
	}
	if err != nil {
		return Variable{}, fmt.Errorf("variable: scan: %w", err)
	}
	return v, nil
}

func scanVariableRows(rows *sql.Rows) (Variable, error) {
	v, err := scanVariableCommon(rows)
	if err != nil {
		return Variable{}, fmt.Errorf("variable: scan: %w", err)
	}
	return v, nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
