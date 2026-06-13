// Workspace mount verbs (sty_1ede3853) — a workspace admin mounts an existing
// repo into their workspace as readonly reference corpus. The mount grants read
// (never write) to the mounting workspace's members; the repo stays writable
// only in its home workspace (project.workspace_id), so there is no "writable
// mount." Add/remove are workspace-admin-gated; list is member-readable.

package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/workspace"
)

type WorkspaceMountAddRequest struct {
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id"`
	Access      string `json:"access,omitempty"` // only "read"/"readonly" accepted
}

type WorkspaceMountRemoveRequest struct {
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id"`
}

type WorkspaceMountListRequest struct {
	WorkspaceID string `json:"workspace_id"`
}

// MountView enriches a mount row with the mounted project's display fields so
// the portal can render it without a second lookup.
type MountView struct {
	WorkspaceID     string `json:"workspace_id"`
	ProjectID       string `json:"project_id"`
	Access          string `json:"access"`
	Name            string `json:"name"`
	Status          string `json:"status"`
	HomeWorkspaceID string `json:"home_workspace_id"`
}

type WorkspaceMountListResponse struct {
	Mounts []MountView `json:"mounts"`
}

func init() {
	Register(&Verb{
		Name:        "workspace_mount_add",
		Description: "Mount an existing repo into a workspace as readonly reference (workspace owner/admin only).",
		Invoke:      invokeWorkspaceMountAdd,
	})
	Register(&Verb{
		Name:        "workspace_mount_remove",
		Description: "Remove a readonly repo mount from a workspace (workspace owner/admin only).",
		Invoke:      invokeWorkspaceMountRemove,
	})
	Register(&Verb{
		Name:        "workspace_mount_list",
		Description: "List the readonly repo mounts in a workspace.",
		Invoke:      invokeWorkspaceMountList,
	})
}

func invokeWorkspaceMountAdd(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if workspaceStore == nil || projectStore == nil {
		return nil, fmt.Errorf("workspace_mount_add: store not configured")
	}
	var req WorkspaceMountAddRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("workspace_mount_add: bad request: %w", err)
		}
	}
	wsID := strings.TrimSpace(req.WorkspaceID)
	pjID := strings.TrimSpace(req.ProjectID)
	if wsID == "" || pjID == "" {
		return nil, fmt.Errorf("workspace_mount_add: workspace_id and project_id required")
	}
	// Mounts are readonly-only — the writable-single-home invariant. Reject any
	// other access so there is no "writable mount" (sty_1ede3853).
	if a := strings.ToLower(strings.TrimSpace(req.Access)); a != "" && a != workspace.AccessRead && a != "readonly" {
		return nil, fmt.Errorf("workspace_mount_add: %w: mounts are readonly (access %q not allowed)", ErrBadRequest, req.Access)
	}
	if authStore != nil && !canManageWorkspace(ctx, wsID) {
		return nil, fmt.Errorf("workspace_mount_add: %w: not an admin of workspace %s", ErrForbidden, wsID)
	}
	// The project must exist; refuse mounting it into its own home — the home
	// already grants full access, so a self-mount is meaningless.
	pj, err := projectStore.GetByID(ctx, pjID)
	if err != nil {
		return nil, fmt.Errorf("workspace_mount_add: project: %w", err)
	}
	if pj.WorkspaceID == wsID {
		return nil, fmt.Errorf("workspace_mount_add: %w: project %s is already home to workspace %s", ErrBadRequest, pjID, wsID)
	}
	if err := workspaceStore.AddMount(ctx, wsID, pjID, callerUserID(ctx), time.Now().UTC()); err != nil {
		return nil, err
	}
	return json.Marshal(MountView{
		WorkspaceID: wsID, ProjectID: pjID, Access: workspace.AccessRead,
		Name: pj.Name, Status: pj.Status, HomeWorkspaceID: pj.WorkspaceID,
	})
}

func invokeWorkspaceMountRemove(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if workspaceStore == nil {
		return nil, fmt.Errorf("workspace_mount_remove: store not configured")
	}
	var req WorkspaceMountRemoveRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("workspace_mount_remove: bad request: %w", err)
		}
	}
	wsID := strings.TrimSpace(req.WorkspaceID)
	pjID := strings.TrimSpace(req.ProjectID)
	if wsID == "" || pjID == "" {
		return nil, fmt.Errorf("workspace_mount_remove: workspace_id and project_id required")
	}
	if authStore != nil && !canManageWorkspace(ctx, wsID) {
		return nil, fmt.Errorf("workspace_mount_remove: %w: not an admin of workspace %s", ErrForbidden, wsID)
	}
	if err := workspaceStore.RemoveMount(ctx, wsID, pjID); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"workspace_id": wsID, "project_id": pjID, "removed": true})
}

func invokeWorkspaceMountList(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if workspaceStore == nil || projectStore == nil {
		return nil, fmt.Errorf("workspace_mount_list: store not configured")
	}
	var req WorkspaceMountListRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("workspace_mount_list: bad request: %w", err)
		}
	}
	wsID := strings.TrimSpace(req.WorkspaceID)
	if wsID == "" {
		return nil, fmt.Errorf("workspace_mount_list: workspace_id required")
	}
	// Read surface: a workspace member or a global admin.
	if authStore != nil && !isWorkspaceMember(ctx, wsID) {
		return nil, fmt.Errorf("workspace_mount_list: %w: not a member of workspace %s", ErrForbidden, wsID)
	}
	mounts, err := workspaceStore.ListMounts(ctx, wsID)
	if err != nil {
		return nil, err
	}
	views := make([]MountView, 0, len(mounts))
	for _, m := range mounts {
		v := MountView{WorkspaceID: m.WorkspaceID, ProjectID: m.ProjectID, Access: m.Access}
		if pj, err := projectStore.GetByID(ctx, m.ProjectID); err == nil {
			v.Name = pj.Name
			v.Status = pj.Status
			v.HomeWorkspaceID = pj.WorkspaceID
		}
		views = append(views, v)
	}
	return json.Marshal(WorkspaceMountListResponse{Mounts: views})
}
