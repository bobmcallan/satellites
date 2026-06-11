package project

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned when a project lookup misses.
var ErrNotFound = errors.New("project: not found")

// Store wraps DB-backed project operations against the projects table
// (migration 0006).
type Store struct {
	DB *sql.DB
}

// New returns a Store bound to the given database/sql handle.
func New(db *sql.DB) *Store { return &Store{DB: db} }

// CreateInput is the set of fields accepted by Create. workspace_id is
// required; gitURL is optional and canonicalised before insert.
type CreateInput struct {
	WorkspaceID string
	Name        string
	Description string
	GitURL      string
	OwnerUserID string
}

// Create inserts a new project. Returns the persisted row including
// the minted id, stamped timestamps, and canonicalised git URL.
func (s *Store) Create(ctx context.Context, in CreateInput, now time.Time) (Project, error) {
	if in.WorkspaceID == "" {
		return Project{}, fmt.Errorf("project: workspace_id required")
	}
	if in.Name == "" {
		return Project{}, fmt.Errorf("project: name required")
	}
	canon, err := CanonicaliseGitRemote(in.GitURL)
	if err != nil {
		return Project{}, err
	}
	id := NewID()
	now = now.UTC()
	var owner sql.NullString
	if in.OwnerUserID != "" {
		owner = sql.NullString{String: in.OwnerUserID, Valid: true}
	}
	if _, err := s.DB.ExecContext(ctx, `
        INSERT INTO projects
            (id, workspace_id, name, description, git_url_canonical,
             owner_user_id, status, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
    `, id, in.WorkspaceID, in.Name, in.Description, canon, owner, StatusActive, now); err != nil {
		return Project{}, fmt.Errorf("project: insert: %w", err)
	}
	return Project{
		ID:              id,
		WorkspaceID:     in.WorkspaceID,
		Name:            in.Name,
		Description:     in.Description,
		GitURLCanonical: canon,
		OwnerUserID:     in.OwnerUserID,
		Status:          StatusActive,
		CreatedAt:       now,
		UpdatedAt:       now,
		NonRepo:         DeriveNonRepo(canon),
	}, nil
}

// GetByID returns the project with the given id, or ErrNotFound.
func (s *Store) GetByID(ctx context.Context, id string) (Project, error) {
	row := s.DB.QueryRowContext(ctx, selectColumns+" FROM projects WHERE id = $1", id)
	return scanRow(row)
}

// GetByGitURL canonicalises the input and returns the project whose
// stored git_url_canonical matches, or ErrNotFound. Empty input is
// treated as "not found" so callers don't need to guard upstream.
func (s *Store) GetByGitURL(ctx context.Context, gitURL string) (Project, error) {
	canon, err := CanonicaliseGitRemote(gitURL)
	if err != nil {
		return Project{}, err
	}
	if canon == "" {
		return Project{}, ErrNotFound
	}
	row := s.DB.QueryRowContext(ctx,
		selectColumns+" FROM projects WHERE git_url_canonical = $1", canon)
	return scanRow(row)
}

// ListByWorkspace returns every project bound to the given workspace
// id, newest-first by created_at. Empty workspaceID lists every
// project across the system (used by admin tooling — guard via
// callers).
func (s *Store) ListByWorkspace(ctx context.Context, workspaceID string) ([]Project, error) {
	q := selectColumns + " FROM projects"
	args := []any{}
	if workspaceID != "" {
		q += " WHERE workspace_id = $1"
		args = append(args, workspaceID)
	}
	q += " ORDER BY created_at DESC, id"
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("project: list: %w", err)
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		p, err := scanRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UpdateInput carries the mutable fields project_update accepts.
// Each pointer field is "set when non-nil"; a non-nil *string with
// empty value clears the column. workspace_id is immutable here —
// cascade-move to a different workspace will be a separate verb.
type UpdateInput struct {
	Name        *string
	Description *string
	GitURL      *string
}

// Update mutates the project row at id. Returns the updated row.
func (s *Store) Update(ctx context.Context, id string, in UpdateInput, now time.Time) (Project, error) {
	now = now.UTC()
	p, err := s.GetByID(ctx, id)
	if err != nil {
		return Project{}, err
	}
	if in.Name != nil {
		if *in.Name == "" {
			return Project{}, fmt.Errorf("project: name cannot be cleared")
		}
		p.Name = *in.Name
	}
	if in.Description != nil {
		p.Description = *in.Description
	}
	if in.GitURL != nil {
		canon, err := CanonicaliseGitRemote(*in.GitURL)
		if err != nil {
			return Project{}, err
		}
		p.GitURLCanonical = canon
	}
	p.UpdatedAt = now
	if _, err := s.DB.ExecContext(ctx, `
        UPDATE projects
        SET name = $1, description = $2, git_url_canonical = $3, updated_at = $4
        WHERE id = $5
    `, p.Name, p.Description, p.GitURLCanonical, now, id); err != nil {
		return Project{}, fmt.Errorf("project: update: %w", err)
	}
	// git_url may have changed above; recompute the derived non_repo signal.
	p.NonRepo = DeriveNonRepo(p.GitURLCanonical)
	return p, nil
}

const selectColumns = `SELECT id, workspace_id, name, description,
    git_url_canonical, owner_user_id, status, created_at, updated_at,
    seed_md, seed_updated_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanCommon(s rowScanner) (Project, error) {
	var (
		p         Project
		owner     sql.NullString
		seedAtRow sql.NullTime
	)
	if err := s.Scan(&p.ID, &p.WorkspaceID, &p.Name, &p.Description,
		&p.GitURLCanonical, &owner, &p.Status, &p.CreatedAt, &p.UpdatedAt,
		&p.SeedMD, &seedAtRow); err != nil {
		return Project{}, err
	}
	if owner.Valid {
		p.OwnerUserID = owner.String
	}
	if seedAtRow.Valid {
		t := seedAtRow.Time
		p.SeedUpdatedAt = &t
	}
	// Derived, not stored: a project with no bound git remote is non-repo.
	p.NonRepo = DeriveNonRepo(p.GitURLCanonical)
	return p, nil
}

// ApplySeed writes body into seed_md and bumps seed_updated_at +
// updated_at on the project at id. Idempotent: when body matches the
// stored seed_md byte-for-byte, no write occurs and the second return
// value (changed) is false.
//
// Sole caller is the project_seed_apply verb, driven by
// `satellites seed push` walking
// `.satellites/seeds/<wksp>/<proj>/project.md`.
func (s *Store) ApplySeed(ctx context.Context, id, body string, now time.Time) (Project, bool, error) {
	now = now.UTC()
	p, err := s.GetByID(ctx, id)
	if err != nil {
		return Project{}, false, err
	}
	if p.SeedMD == body {
		return p, false, nil
	}
	if _, err := s.DB.ExecContext(ctx, `
        UPDATE projects
        SET seed_md = $1, seed_updated_at = $2, updated_at = $2
        WHERE id = $3
    `, body, now, id); err != nil {
		return Project{}, false, fmt.Errorf("project: apply seed: %w", err)
	}
	p.SeedMD = body
	p.SeedUpdatedAt = &now
	p.UpdatedAt = now
	return p, true, nil
}

func scanRow(row *sql.Row) (Project, error) {
	p, err := scanCommon(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, fmt.Errorf("project: scan: %w", err)
	}
	return p, nil
}

func scanRows(rows *sql.Rows) (Project, error) {
	p, err := scanCommon(rows)
	if err != nil {
		return Project{}, fmt.Errorf("project: scan: %w", err)
	}
	return p, nil
}
