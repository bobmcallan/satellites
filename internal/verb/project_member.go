// Project membership verbs — epic:user-admin (sty_3dbd53f0), mirroring the
// workspace_member_* surface. A project admin (or workspace owner/admin, or
// global admin — all fold in via effectiveProjectRole) curates a project's
// member list. CLI + portal handlers only; kept off the MCP surface per
// no-new-mcp-verbs.

package verb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/workspace"
)

type ProjectMemberAddRequest struct {
	ProjectID string `json:"project_id"`
	UserID    string `json:"user_id"`
	Role      string `json:"role"`
}

type ProjectMemberListRequest struct {
	ProjectID string `json:"project_id"`
}

type ProjectMemberListResponse struct {
	Members []project.Member `json:"members"`
}

type ProjectMemberUpdateRequest struct {
	ProjectID string `json:"project_id"`
	UserID    string `json:"user_id"`
	Role      string `json:"role"`
}

type ProjectMemberRemoveRequest struct {
	ProjectID string `json:"project_id"`
	UserID    string `json:"user_id"`
}

// ProjectAccessRequest is the input to project_access.
type ProjectAccessRequest struct {
	ProjectID string `json:"project_id"`
}

// ProjectAccessResponse reports the caller's effective project role.
type ProjectAccessResponse struct {
	ProjectID string `json:"project_id"`
	Role      string `json:"role"` // admin|write|read|"" (none)
}

func init() {
	Register(&Verb{
		Name:        "project_access",
		Description: "Return the caller's effective role on a project (admin|write|read|none).",
		Invoke:      invokeProjectAccess,
	})
	Register(&Verb{
		Name:        "project_member_add",
		Description: "Add a user to a project at the given role (read|write|admin); upserts.",
		Invoke:      invokeProjectMemberAdd,
	})
	Register(&Verb{
		Name:        "project_member_list",
		Description: "List members of a project, oldest-first.",
		Invoke:      invokeProjectMemberList,
	})
	Register(&Verb{
		Name:        "project_member_update_role",
		Description: "Change a member's role on a project.",
		Invoke:      invokeProjectMemberUpdateRole,
	})
	Register(&Verb{
		Name:        "project_member_remove",
		Description: "Remove a member from a project.",
		Invoke:      invokeProjectMemberRemove,
	})
}

// requireProjectAdmin gates a project-member mutation: the caller must be a
// project admin (which folds in workspace owner/admin + global admin). CLI-local
// callers (no auth wiring) bypass.
func requireProjectAdmin(ctx context.Context, projectID, verbName string) error {
	if authStore == nil {
		return nil
	}
	if !effectiveProjectRoleAtLeast(ctx, projectID, project.RoleAdmin) {
		return fmt.Errorf("%s: %w: project admin required for %s", verbName, ErrForbidden, projectID)
	}
	return nil
}

func invokeProjectAccess(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var req ProjectAccessRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("project_access: bad request: %w", err)
		}
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		return nil, fmt.Errorf("project_access: project_id required")
	}
	return json.Marshal(ProjectAccessResponse{
		ProjectID: req.ProjectID,
		Role:      effectiveProjectRole(ctx, req.ProjectID),
	})
}

func invokeProjectMemberAdd(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if projectStore == nil {
		return nil, fmt.Errorf("project_member_add: store not configured")
	}
	var req ProjectMemberAddRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("project_member_add: bad request: %w", err)
		}
	}
	if strings.TrimSpace(req.ProjectID) == "" || strings.TrimSpace(req.UserID) == "" {
		return nil, fmt.Errorf("project_member_add: project_id and user_id required")
	}
	if strings.TrimSpace(req.Role) == "" {
		return nil, fmt.Errorf("project_member_add: role required")
	}
	if err := requireProjectAdmin(ctx, req.ProjectID, "project_member_add"); err != nil {
		return nil, err
	}
	addedBy := callerUserID(ctx)
	if err := projectStore.AddMember(ctx, req.ProjectID, req.UserID, req.Role, addedBy, time.Now().UTC()); err != nil {
		if errors.Is(err, project.ErrInvalidRole) {
			return nil, fmt.Errorf("project_member_add: invalid_role")
		}
		return nil, err
	}
	role, err := projectStore.GetRole(ctx, req.ProjectID, req.UserID)
	if err != nil {
		return nil, err
	}
	return json.Marshal(project.Member{
		ProjectID: req.ProjectID, UserID: req.UserID, Role: role,
		AddedAt: time.Now().UTC(), AddedBy: addedBy,
	})
}

func invokeProjectMemberList(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if projectStore == nil {
		return nil, fmt.Errorf("project_member_list: store not configured")
	}
	var req ProjectMemberListRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("project_member_list: bad request: %w", err)
		}
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		return nil, fmt.Errorf("project_member_list: project_id required")
	}
	// Any project member (≥ read) may see the roster.
	if authStore != nil && !effectiveProjectRoleAtLeast(ctx, req.ProjectID, project.RoleRead) {
		return nil, fmt.Errorf("project_member_list: %w: no access to project %s", ErrForbidden, req.ProjectID)
	}
	ms, err := projectStore.ListMembers(ctx, req.ProjectID)
	if err != nil {
		return nil, err
	}
	for i := range ms {
		ms[i].Source = project.SourceProject
	}
	// Auto-member (sty_1c266e21): the project's workspace owners/admins administer
	// it, so they appear in the roster as implicit members even without an
	// explicit project_members row. Explicit rows win on conflict.
	ms = append(ms, inheritedWorkspaceAdmins(ctx, req.ProjectID, ms)...)
	if ms == nil {
		ms = []project.Member{}
	}
	return json.Marshal(ProjectMemberListResponse{Members: ms})
}

// inheritedWorkspaceAdmins returns the project's workspace owners/admins as
// implicit project members (role admin, source workspace), excluding any user
// already present as an explicit member. Best-effort: an unwired store or a
// lookup miss simply contributes nothing.
func inheritedWorkspaceAdmins(ctx context.Context, projectID string, explicit []project.Member) []project.Member {
	if projectStore == nil || workspaceStore == nil {
		return nil
	}
	pj, err := projectStore.GetByID(ctx, projectID)
	if err != nil || pj.WorkspaceID == "" {
		return nil
	}
	wsMembers, err := workspaceStore.ListMembers(ctx, pj.WorkspaceID)
	if err != nil {
		return nil
	}
	seen := make(map[string]bool, len(explicit))
	for _, m := range explicit {
		seen[m.UserID] = true
	}
	var out []project.Member
	for _, wm := range wsMembers {
		if wm.Role != workspace.RoleOwner && wm.Role != workspace.RoleAdmin {
			continue
		}
		if seen[wm.UserID] {
			continue
		}
		seen[wm.UserID] = true
		out = append(out, project.Member{
			ProjectID: projectID,
			UserID:    wm.UserID,
			Role:      project.RoleAdmin,
			AddedAt:   wm.AddedAt,
			Source:    project.SourceWorkspace,
		})
	}
	return out
}

func invokeProjectMemberUpdateRole(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if projectStore == nil {
		return nil, fmt.Errorf("project_member_update_role: store not configured")
	}
	var req ProjectMemberUpdateRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("project_member_update_role: bad request: %w", err)
		}
	}
	if strings.TrimSpace(req.ProjectID) == "" || strings.TrimSpace(req.UserID) == "" {
		return nil, fmt.Errorf("project_member_update_role: project_id and user_id required")
	}
	if strings.TrimSpace(req.Role) == "" {
		return nil, fmt.Errorf("project_member_update_role: role required")
	}
	if err := requireProjectAdmin(ctx, req.ProjectID, "project_member_update_role"); err != nil {
		return nil, err
	}
	if err := projectStore.UpdateRole(ctx, req.ProjectID, req.UserID, req.Role, time.Now().UTC()); err != nil {
		if errors.Is(err, project.ErrInvalidRole) {
			return nil, fmt.Errorf("project_member_update_role: invalid_role")
		}
		if errors.Is(err, project.ErrMemberNotFound) {
			return nil, fmt.Errorf("project_member_update_role: member_not_found")
		}
		return nil, err
	}
	return json.Marshal(map[string]string{
		"project_id": req.ProjectID, "user_id": req.UserID, "role": req.Role,
	})
}

func invokeProjectMemberRemove(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if projectStore == nil {
		return nil, fmt.Errorf("project_member_remove: store not configured")
	}
	var req ProjectMemberRemoveRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("project_member_remove: bad request: %w", err)
		}
	}
	if strings.TrimSpace(req.ProjectID) == "" || strings.TrimSpace(req.UserID) == "" {
		return nil, fmt.Errorf("project_member_remove: project_id and user_id required")
	}
	if err := requireProjectAdmin(ctx, req.ProjectID, "project_member_remove"); err != nil {
		return nil, err
	}
	if err := projectStore.RemoveMember(ctx, req.ProjectID, req.UserID); err != nil {
		if errors.Is(err, project.ErrMemberNotFound) {
			return nil, fmt.Errorf("project_member_remove: member_not_found")
		}
		return nil, err
	}
	return json.Marshal(map[string]string{
		"project_id": req.ProjectID, "user_id": req.UserID, "removed": "true",
	})
}
