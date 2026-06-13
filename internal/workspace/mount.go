package workspace

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// AccessRead is the only mount access level: a readonly mount grants read,
// never write (sty_1ede3853). A repo is writable only in its home workspace,
// so there is no "writable mount" — the writable-single-home invariant.
const AccessRead = "read"

// Mount binds a readonly view of a project (whose home is another workspace)
// into a workspace, as reference corpus.
type Mount struct {
	WorkspaceID string    `json:"workspace_id"`
	ProjectID   string    `json:"project_id"`
	Access      string    `json:"access"`
	CreatedAt   time.Time `json:"created_at"`
	CreatedBy   string    `json:"created_by,omitempty"`
}

// AddMount inserts or updates a readonly mount of projectID into workspaceID.
// Access is always read — the caller-facing verb rejects any other value.
func (s *Store) AddMount(ctx context.Context, workspaceID, projectID, createdBy string, now time.Time) error {
	if workspaceID == "" || projectID == "" {
		return fmt.Errorf("workspace: workspace_id and project_id required")
	}
	now = now.UTC()
	var byCol sql.NullString
	if createdBy != "" {
		byCol = sql.NullString{String: createdBy, Valid: true}
	}
	_, err := s.DB.ExecContext(ctx, `
        INSERT INTO workspace_mounts (workspace_id, project_id, access, created_at, created_by)
        VALUES ($1, $2, $3, $4, $5)
        ON CONFLICT (workspace_id, project_id)
        DO UPDATE SET access = EXCLUDED.access, created_at = EXCLUDED.created_at, created_by = EXCLUDED.created_by
    `, workspaceID, projectID, AccessRead, now, byCol)
	if err != nil {
		return fmt.Errorf("workspace: add mount: %w", err)
	}
	return nil
}

// RemoveMount deletes a mount; no error when none matched (idempotent).
func (s *Store) RemoveMount(ctx context.Context, workspaceID, projectID string) error {
	if _, err := s.DB.ExecContext(ctx, `
        DELETE FROM workspace_mounts WHERE workspace_id = $1 AND project_id = $2
    `, workspaceID, projectID); err != nil {
		return fmt.Errorf("workspace: remove mount: %w", err)
	}
	return nil
}

// ListMounts returns the readonly mounts into workspaceID, newest-first.
func (s *Store) ListMounts(ctx context.Context, workspaceID string) ([]Mount, error) {
	rows, err := s.DB.QueryContext(ctx, `
        SELECT workspace_id, project_id, access, created_at, created_by
        FROM workspace_mounts WHERE workspace_id = $1
        ORDER BY created_at DESC, project_id
    `, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("workspace: list mounts: %w", err)
	}
	defer rows.Close()
	var out []Mount
	for rows.Next() {
		var (
			m  Mount
			by sql.NullString
		)
		if err := rows.Scan(&m.WorkspaceID, &m.ProjectID, &m.Access, &m.CreatedAt, &by); err != nil {
			return nil, fmt.Errorf("workspace: scan mount: %w", err)
		}
		if by.Valid {
			m.CreatedBy = by.String
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CallerHasMountAccess reports whether userID is a member of ANY workspace that
// readonly-mounts projectID — the read-grant path the project authz resolver
// consults (sty_1ede3853). Read access is sufficient on membership in any
// mounting workspace; the mount never confers more than read.
func (s *Store) CallerHasMountAccess(ctx context.Context, projectID, userID string) (bool, error) {
	if projectID == "" || userID == "" {
		return false, nil
	}
	var exists bool
	if err := s.DB.QueryRowContext(ctx, `
        SELECT EXISTS (
            SELECT 1 FROM workspace_mounts m
            JOIN workspace_members wm ON wm.workspace_id = m.workspace_id
            WHERE m.project_id = $1 AND wm.user_id = $2
        )
    `, projectID, userID).Scan(&exists); err != nil {
		return false, fmt.Errorf("workspace: mount access: %w", err)
	}
	return exists, nil
}
