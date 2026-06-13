package workspace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Role values for a Member. The GitHub-aligned workspace role set
// (epic:user-admin): owner (the creator, exactly one per workspace) >
// admin (manages members/projects/roles) > member (belongs to the
// workspace; project access is governed per-project). The legacy
// reviewer/viewer roles were retired and folded to member by migration
// 0027 — project-level read/write now carries that distinction.
const (
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleMember = "member"
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

// IsValidRole reports whether r is one of the recognised workspace
// roles (owner|admin|member).
func IsValidRole(r string) bool {
	switch r {
	case RoleOwner, RoleAdmin, RoleMember:
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

// ListWorkspaceIDsForUser returns the ids of every workspace the user is a
// member of (any role), unordered. Backs the SSE trigger bus's per-user topic
// scoping (sty_b6e39eb8): a non-admin only receives triggers for workspaces in
// this set. Reads via the workspace_members(user_id) index.
func (s *Store) ListWorkspaceIDsForUser(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `
        SELECT workspace_id FROM workspace_members WHERE user_id = $1
    `, userID)
	if err != nil {
		return nil, fmt.Errorf("workspace: list workspaces for user: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("workspace: scan workspace id: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// HasAdminRole reports whether the user holds an owner or admin role on ANY
// workspace. It backs the coarse "workspace-admin" MCP surface tier: a caller
// who can administer some workspace sees the workspace-admin verbs (the verb's
// own per-workspace check is the fine-grained call-time gate). An empty userID
// is never an admin.
func (s *Store) HasAdminRole(ctx context.Context, userID string) (bool, error) {
	if userID == "" {
		return false, nil
	}
	var exists bool
	err := s.DB.QueryRowContext(ctx, `
        SELECT EXISTS(
            SELECT 1 FROM workspace_members
            WHERE user_id = $1 AND role IN ('owner', 'admin')
        )
    `, userID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("workspace: has admin role: %w", err)
	}
	return exists, nil
}

// CountAdmins returns the number of owner/admin members on the workspace.
// Backs the no-orphaned-admin guard (sty_20687710): a workspace must keep at
// least one owner/admin, so the last one may not be removed or demoted.
func (s *Store) CountAdmins(ctx context.Context, workspaceID string) (int, error) {
	var n int
	if err := s.DB.QueryRowContext(ctx, `
        SELECT COUNT(*) FROM workspace_members
        WHERE workspace_id = $1 AND role IN ('owner', 'admin')
    `, workspaceID).Scan(&n); err != nil {
		return 0, fmt.Errorf("workspace: count admins: %w", err)
	}
	return n, nil
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
