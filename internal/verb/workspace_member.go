// Workspace membership verbs — PR 4. The substrate guarantee is:
// every workspace_create call with a non-empty owner_user_id auto-
// adds the creator as admin; these verbs let admins curate the rest
// of the member list.

package verb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/workspace"
)

type WorkspaceMemberAddRequest struct {
	WorkspaceID string `json:"workspace_id"`
	UserID      string `json:"user_id"`
	Role        string `json:"role"`
}

type WorkspaceMemberListRequest struct {
	WorkspaceID string `json:"workspace_id"`
}

type WorkspaceMemberListResponse struct {
	Members []workspace.Member `json:"members"`
}

type WorkspaceMemberUpdateRequest struct {
	WorkspaceID string `json:"workspace_id"`
	UserID      string `json:"user_id"`
	Role        string `json:"role"`
}

type WorkspaceMemberRemoveRequest struct {
	WorkspaceID string `json:"workspace_id"`
	UserID      string `json:"user_id"`
}

func init() {
	Register(&Verb{
		Name:        "workspace_member_add",
		Description: "Add a user to a workspace at the given role (upserts).",
		Invoke:      invokeWorkspaceMemberAdd,
		MCPRole:     MCPRoleWorkspaceAdmin,
	})
	Register(&Verb{
		Name:        "workspace_member_list",
		Description: "List members of a workspace, oldest-first.",
		Invoke:      invokeWorkspaceMemberList,
	})
	Register(&Verb{
		Name:        "workspace_member_update_role",
		Description: "Change a member's role on a workspace.",
		Invoke:      invokeWorkspaceMemberUpdateRole,
		MCPRole:     MCPRoleWorkspaceAdmin,
	})
	Register(&Verb{
		Name:        "workspace_member_remove",
		Description: "Remove a member from a workspace.",
		Invoke:      invokeWorkspaceMemberRemove,
		MCPRole:     MCPRoleWorkspaceAdmin,
	})
}

func invokeWorkspaceMemberAdd(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if workspaceStore == nil {
		return nil, fmt.Errorf("workspace_member_add: store not configured")
	}
	var req WorkspaceMemberAddRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("workspace_member_add: bad request: %w", err)
		}
	}
	if strings.TrimSpace(req.WorkspaceID) == "" || strings.TrimSpace(req.UserID) == "" {
		return nil, fmt.Errorf("workspace_member_add: workspace_id and user_id required")
	}
	if strings.TrimSpace(req.Role) == "" {
		return nil, fmt.Errorf("workspace_member_add: role required")
	}
	if authStore != nil && !canManageWorkspace(ctx, req.WorkspaceID) {
		return nil, fmt.Errorf("workspace_member_add: %w: not an admin of workspace %s", ErrForbidden, req.WorkspaceID)
	}
	// Role ceiling: a granter may not grant above their own role (sty_20687710).
	if authStore != nil && !callerWorkspaceRoleCeilingOK(ctx, req.WorkspaceID, req.Role) {
		return nil, fmt.Errorf("workspace_member_add: %w: cannot grant a role above your own", ErrForbidden)
	}
	addedBy := callerUserID(ctx)
	if err := workspaceStore.AddMember(ctx, req.WorkspaceID, req.UserID, req.Role, addedBy, time.Now().UTC()); err != nil {
		if errors.Is(err, workspace.ErrInvalidRole) {
			return nil, fmt.Errorf("workspace_member_add: invalid_role")
		}
		return nil, err
	}
	role, err := workspaceStore.GetRole(ctx, req.WorkspaceID, req.UserID)
	if err != nil {
		return nil, err
	}
	// A grant change can alter the affected user's role-filtered MCP tool
	// list — nudge connected clients to re-list (the gate still fails closed).
	notifyToolsListChanged()
	return json.Marshal(workspace.Member{
		WorkspaceID: req.WorkspaceID, UserID: req.UserID, Role: role,
		AddedAt: time.Now().UTC(), AddedBy: addedBy,
	})
}

func invokeWorkspaceMemberList(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if workspaceStore == nil {
		return nil, fmt.Errorf("workspace_member_list: store not configured")
	}
	var req WorkspaceMemberListRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("workspace_member_list: bad request: %w", err)
		}
	}
	if strings.TrimSpace(req.WorkspaceID) == "" {
		return nil, fmt.Errorf("workspace_member_list: workspace_id required")
	}
	if authStore != nil && !isWorkspaceMember(ctx, req.WorkspaceID) {
		return nil, fmt.Errorf("workspace_member_list: %w: not a member of workspace %s", ErrForbidden, req.WorkspaceID)
	}
	ms, err := workspaceStore.ListMembers(ctx, req.WorkspaceID)
	if err != nil {
		return nil, err
	}
	if ms == nil {
		ms = []workspace.Member{}
	}
	// Resolve each member's human-readable display name server-side from the
	// persisted user record (sty_fcf5477e). No email is returned — only the
	// resolved name — so the raw user id is no longer the primary label and no
	// address leaks. CLI-local (no authStore) falls back to the id via the chain.
	for i := range ms {
		var u *auth.User
		if authStore != nil {
			u, _ = authStore.GetUserByID(ctx, ms[i].UserID)
		}
		ms[i].Name = auth.DisplayName(u, ms[i].UserID)
	}
	return json.Marshal(WorkspaceMemberListResponse{Members: ms})
}

func invokeWorkspaceMemberUpdateRole(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if workspaceStore == nil {
		return nil, fmt.Errorf("workspace_member_update_role: store not configured")
	}
	var req WorkspaceMemberUpdateRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("workspace_member_update_role: bad request: %w", err)
		}
	}
	if strings.TrimSpace(req.WorkspaceID) == "" || strings.TrimSpace(req.UserID) == "" {
		return nil, fmt.Errorf("workspace_member_update_role: workspace_id and user_id required")
	}
	if strings.TrimSpace(req.Role) == "" {
		return nil, fmt.Errorf("workspace_member_update_role: role required")
	}
	if authStore != nil && !canManageWorkspace(ctx, req.WorkspaceID) {
		return nil, fmt.Errorf("workspace_member_update_role: %w: not an admin of workspace %s", ErrForbidden, req.WorkspaceID)
	}
	// Role ceiling: cannot promote above the granter's own role (sty_20687710).
	if authStore != nil && !callerWorkspaceRoleCeilingOK(ctx, req.WorkspaceID, req.Role) {
		return nil, fmt.Errorf("workspace_member_update_role: %w: cannot grant a role above your own", ErrForbidden)
	}
	// No orphaned admin: refuse demoting the LAST owner/admin below admin.
	if authStore != nil && workspaceRoleRank(req.Role) < workspaceRoleRank(workspace.RoleAdmin) {
		if cur, err := workspaceStore.GetRole(ctx, req.WorkspaceID, req.UserID); err == nil &&
			(cur == workspace.RoleOwner || cur == workspace.RoleAdmin) {
			if n, err := workspaceStore.CountAdmins(ctx, req.WorkspaceID); err == nil && n <= 1 {
				return nil, fmt.Errorf("workspace_member_update_role: %w: cannot demote the last owner/admin of workspace %s", ErrForbidden, req.WorkspaceID)
			}
		}
	}
	if err := workspaceStore.UpdateRole(ctx, req.WorkspaceID, req.UserID, req.Role, time.Now().UTC()); err != nil {
		if errors.Is(err, workspace.ErrInvalidRole) {
			return nil, fmt.Errorf("workspace_member_update_role: invalid_role")
		}
		if errors.Is(err, workspace.ErrMemberNotFound) {
			return nil, fmt.Errorf("workspace_member_update_role: member_not_found")
		}
		return nil, err
	}
	notifyToolsListChanged()
	return json.Marshal(map[string]string{
		"workspace_id": req.WorkspaceID, "user_id": req.UserID, "role": req.Role,
	})
}

func invokeWorkspaceMemberRemove(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if workspaceStore == nil {
		return nil, fmt.Errorf("workspace_member_remove: store not configured")
	}
	var req WorkspaceMemberRemoveRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("workspace_member_remove: bad request: %w", err)
		}
	}
	if strings.TrimSpace(req.WorkspaceID) == "" || strings.TrimSpace(req.UserID) == "" {
		return nil, fmt.Errorf("workspace_member_remove: workspace_id and user_id required")
	}
	if authStore != nil && !canManageWorkspace(ctx, req.WorkspaceID) {
		return nil, fmt.Errorf("workspace_member_remove: %w: not an admin of workspace %s", ErrForbidden, req.WorkspaceID)
	}
	// No orphaned admin: refuse removing the LAST owner/admin of the workspace.
	if authStore != nil {
		if cur, err := workspaceStore.GetRole(ctx, req.WorkspaceID, req.UserID); err == nil &&
			(cur == workspace.RoleOwner || cur == workspace.RoleAdmin) {
			if n, err := workspaceStore.CountAdmins(ctx, req.WorkspaceID); err == nil && n <= 1 {
				return nil, fmt.Errorf("workspace_member_remove: %w: cannot remove the last owner/admin of workspace %s", ErrForbidden, req.WorkspaceID)
			}
		}
	}
	if err := workspaceStore.RemoveMember(ctx, req.WorkspaceID, req.UserID); err != nil {
		if errors.Is(err, workspace.ErrMemberNotFound) {
			return nil, fmt.Errorf("workspace_member_remove: member_not_found")
		}
		return nil, err
	}
	notifyToolsListChanged()
	return json.Marshal(map[string]string{
		"workspace_id": req.WorkspaceID, "user_id": req.UserID, "removed": "true",
	})
}
