package workspace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Role values for a Member.
const (
	RoleAdmin    = "admin"
	RoleMember   = "member"
	RoleReviewer = "reviewer"
	RoleViewer   = "viewer"
)

// ErrInvalidRole is returned when an unknown role string is supplied.
var ErrInvalidRole = errors.New("workspace: invalid role")

// ErrMemberNotFound is returned by member-specific operations when no
// row matches (workspace_id, user_id).
var ErrMemberNotFound = errors.New("workspace: member not found")

// Member binds a user to a workspace at a role.
type Member struct {
	WorkspaceID string    `json:"workspace_id"`
	UserID      string    `json:"user_id"`
	Role        string    `json:"role"`
	AddedAt     time.Time `json:"added_at"`
	AddedBy     string    `json:"added_by,omitempty"`
}

// IsValidRole reports whether r is one of the four recognised roles.
func IsValidRole(r string) bool {
	switch r {
	case RoleAdmin, RoleMember, RoleReviewer, RoleViewer:
		return true
	}
	return false
}

// AddMember inserts or updates a membership row. Role is validated
// here so handlers can rely on substrate-level guarantees.
func (s *Store) AddMember(ctx context.Context, workspaceID, userID, role, addedBy string, now time.Time) error {
	if workspaceID == "" || userID == "" {
		return fmt.Errorf("workspace: workspace_id and user_id required")
	}
	if !IsValidRole(role) {
		return ErrInvalidRole
	}
	now = now.UTC()
	var addedByCol sql.NullString
	if addedBy != "" {
		addedByCol = sql.NullString{String: addedBy, Valid: true}
	}
	_, err := s.DB.ExecContext(ctx, `
        INSERT INTO workspace_members (workspace_id, user_id, role, added_at, added_by)
        VALUES ($1, $2, $3, $4, $5)
        ON CONFLICT (workspace_id, user_id)
        DO UPDATE SET role = EXCLUDED.role, added_at = EXCLUDED.added_at, added_by = EXCLUDED.added_by
    `, workspaceID, userID, role, now, addedByCol)
	if err != nil {
		return fmt.Errorf("workspace: add member: %w", err)
	}
	return nil
}

// ListMembers returns all membership rows on a workspace, ordered by
// added_at ascending (creator/admin first).
func (s *Store) ListMembers(ctx context.Context, workspaceID string) ([]Member, error) {
	rows, err := s.DB.QueryContext(ctx, `
        SELECT workspace_id, user_id, role, added_at, added_by
        FROM workspace_members
        WHERE workspace_id = $1
        ORDER BY added_at ASC, user_id
    `, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("workspace: list members: %w", err)
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var (
			m       Member
			addedBy sql.NullString
		)
		if err := rows.Scan(&m.WorkspaceID, &m.UserID, &m.Role, &m.AddedAt, &addedBy); err != nil {
			return nil, fmt.Errorf("workspace: scan member: %w", err)
		}
		if addedBy.Valid {
			m.AddedBy = addedBy.String
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UpdateRole mutates an existing member's role. ErrMemberNotFound when
// no row matches; ErrInvalidRole on an unknown role string.
func (s *Store) UpdateRole(ctx context.Context, workspaceID, userID, newRole string, now time.Time) error {
	if !IsValidRole(newRole) {
		return ErrInvalidRole
	}
	res, err := s.DB.ExecContext(ctx, `
        UPDATE workspace_members SET role = $1, added_at = $2
        WHERE workspace_id = $3 AND user_id = $4
    `, newRole, now.UTC(), workspaceID, userID)
	if err != nil {
		return fmt.Errorf("workspace: update role: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("workspace: update role rows: %w", err)
	}
	if n == 0 {
		return ErrMemberNotFound
	}
	return nil
}

// RemoveMember deletes a membership row. ErrMemberNotFound when the
// user isn't a member.
func (s *Store) RemoveMember(ctx context.Context, workspaceID, userID string) error {
	res, err := s.DB.ExecContext(ctx, `
        DELETE FROM workspace_members WHERE workspace_id = $1 AND user_id = $2
    `, workspaceID, userID)
	if err != nil {
		return fmt.Errorf("workspace: remove member: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("workspace: remove member rows: %w", err)
	}
	if n == 0 {
		return ErrMemberNotFound
	}
	return nil
}

// GetRole returns the member's role on the workspace, or
// ErrMemberNotFound.
func (s *Store) GetRole(ctx context.Context, workspaceID, userID string) (string, error) {
	row := s.DB.QueryRowContext(ctx, `
        SELECT role FROM workspace_members WHERE workspace_id = $1 AND user_id = $2
    `, workspaceID, userID)
	var role string
	if err := row.Scan(&role); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrMemberNotFound
		}
		return "", fmt.Errorf("workspace: get role: %w", err)
	}
	return role, nil
}
