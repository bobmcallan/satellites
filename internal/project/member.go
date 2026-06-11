package project

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Project role values for a Member. The GitHub-aligned project role set
// (epic:user-admin), modelling repo collaborators: read (view the project
// + its stories/docs) < write (create/update stories & documents at
// project scope) < admin (write + manage the project's members/settings).
// A workspace owner/admin (and any global admin) is implicitly project
// admin; explicit project_members rows grant access to specific members.
// Enforcement of this matrix lands in epic-order:4 — this is the vocabulary.
const (
	RoleRead  = "read"
	RoleWrite = "write"
	RoleAdmin = "admin"
)

// ErrInvalidRole is returned when an unknown project role string is supplied.
var ErrInvalidRole = errors.New("project: invalid role")

// ErrMemberNotFound is returned by member-specific operations when no row
// matches (project_id, user_id).
var ErrMemberNotFound = errors.New("project: member not found")

// IsValidProjectRole reports whether r is one of the recognised project
// roles (read|write|admin).
func IsValidProjectRole(r string) bool {
	switch r {
	case RoleRead, RoleWrite, RoleAdmin:
		return true
	}
	return false
}

// Member binds a user to a project at a role. Mirrors
// workspace.Member (internal/workspace/member.go); the two membership
// layers are deliberately parallel.
type Member struct {
	ProjectID string    `json:"project_id"`
	UserID    string    `json:"user_id"`
	Role      string    `json:"role"`
	AddedAt   time.Time `json:"added_at"`
	AddedBy   string    `json:"added_by,omitempty"`
	// Source distinguishes an explicit project_members row ("project") from a
	// membership inherited from the workspace ("workspace" — owner/admin). Empty
	// on rows read straight from project_members; set by the roster union.
	Source string `json:"source,omitempty"`
}

// Membership sources for Member.Source.
const (
	SourceProject   = "project"
	SourceWorkspace = "workspace"
)

// AddMember inserts or updates a project membership row. Role is
// validated here so handlers can rely on substrate-level guarantees.
func (s *Store) AddMember(ctx context.Context, projectID, userID, role, addedBy string, now time.Time) error {
	if projectID == "" || userID == "" {
		return fmt.Errorf("project: project_id and user_id required")
	}
	if !IsValidProjectRole(role) {
		return ErrInvalidRole
	}
	now = now.UTC()
	var addedByCol sql.NullString
	if addedBy != "" {
		addedByCol = sql.NullString{String: addedBy, Valid: true}
	}
	_, err := s.DB.ExecContext(ctx, `
        INSERT INTO project_members (project_id, user_id, role, added_at, added_by)
        VALUES ($1, $2, $3, $4, $5)
        ON CONFLICT (project_id, user_id)
        DO UPDATE SET role = EXCLUDED.role, added_at = EXCLUDED.added_at, added_by = EXCLUDED.added_by
    `, projectID, userID, role, now, addedByCol)
	if err != nil {
		return fmt.Errorf("project: add member: %w", err)
	}
	return nil
}

// ListMembers returns all membership rows on a project, ordered by
// added_at ascending (oldest first).
func (s *Store) ListMembers(ctx context.Context, projectID string) ([]Member, error) {
	rows, err := s.DB.QueryContext(ctx, `
        SELECT project_id, user_id, role, added_at, added_by
        FROM project_members
        WHERE project_id = $1
        ORDER BY added_at ASC, user_id
    `, projectID)
	if err != nil {
		return nil, fmt.Errorf("project: list members: %w", err)
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var (
			m       Member
			addedBy sql.NullString
		)
		if err := rows.Scan(&m.ProjectID, &m.UserID, &m.Role, &m.AddedAt, &addedBy); err != nil {
			return nil, fmt.Errorf("project: scan member: %w", err)
		}
		if addedBy.Valid {
			m.AddedBy = addedBy.String
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UpdateRole mutates an existing member's role. ErrMemberNotFound when no
// row matches; ErrInvalidRole on an unknown role string.
func (s *Store) UpdateRole(ctx context.Context, projectID, userID, newRole string, now time.Time) error {
	if !IsValidProjectRole(newRole) {
		return ErrInvalidRole
	}
	res, err := s.DB.ExecContext(ctx, `
        UPDATE project_members SET role = $1, added_at = $2
        WHERE project_id = $3 AND user_id = $4
    `, newRole, now.UTC(), projectID, userID)
	if err != nil {
		return fmt.Errorf("project: update role: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("project: update role rows: %w", err)
	}
	if n == 0 {
		return ErrMemberNotFound
	}
	return nil
}

// RemoveMember deletes a project membership row. ErrMemberNotFound when
// the user isn't a member.
func (s *Store) RemoveMember(ctx context.Context, projectID, userID string) error {
	res, err := s.DB.ExecContext(ctx, `
        DELETE FROM project_members WHERE project_id = $1 AND user_id = $2
    `, projectID, userID)
	if err != nil {
		return fmt.Errorf("project: remove member: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("project: remove member rows: %w", err)
	}
	if n == 0 {
		return ErrMemberNotFound
	}
	return nil
}

// GetRole returns the member's explicit role on the project, or
// ErrMemberNotFound. This is the explicit-grant lookup only; the
// effective-role resolver (epic-order:4) composes this with global-admin
// and workspace owner/admin inheritance.
func (s *Store) GetRole(ctx context.Context, projectID, userID string) (string, error) {
	row := s.DB.QueryRowContext(ctx, `
        SELECT role FROM project_members WHERE project_id = $1 AND user_id = $2
    `, projectID, userID)
	var role string
	if err := row.Scan(&role); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrMemberNotFound
		}
		return "", fmt.Errorf("project: get role: %w", err)
	}
	return role, nil
}
