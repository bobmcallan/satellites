// Workspace verbs — V5's first multi-tenant domain surface.
//
// Each verb dispatches to internal/workspace.Store. The server boot
// wires the store via SetWorkspaceStore. CLI-local invocations that
// have no auth context create NULL-owner rows; once
// auth.FromContext(ctx) returns a non-nil user (the MCP path), the
// authenticated user id becomes owner_user_id.

package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/workspace"
)

// workspaceStore is set by the server at boot via SetWorkspaceStore.
// Verbs return an explicit "store not configured" error when nil so
// CLI-local callers running against a process without a DB get a
// readable failure mode.
var workspaceStore *workspace.Store

// SetWorkspaceStore wires the server's workspace.Store into the verb
// package. Called by cmd/satellites-server on boot, after migrations
// and the workspace.SeedDefault call.
func SetWorkspaceStore(s *workspace.Store) { workspaceStore = s }

// WorkspaceCreateRequest is the input to workspace_create.
type WorkspaceCreateRequest struct {
	Name string `json:"name"`
}

// WorkspaceGetRequest is the input to workspace_get and
// workspace_set_default.
type WorkspaceGetRequest struct {
	ID string `json:"id"`
}

// WorkspaceListResponse wraps the list result so future per-owner
// filters can land additive fields without breaking JSON consumers.
type WorkspaceListResponse struct {
	Workspaces []workspace.Workspace `json:"workspaces"`
}

func init() {
	Register(&Verb{
		Name:        "workspace_create",
		Description: "Create a new workspace.",
		Invoke:      invokeWorkspaceCreate,
	})
	Register(&Verb{
		Name:        "workspace_list",
		Description: "List all workspaces, newest-first.",
		Invoke:      invokeWorkspaceList,
	})
	Register(&Verb{
		Name:        "workspace_get",
		Description: "Fetch a workspace by id.",
		Invoke:      invokeWorkspaceGet,
	})
	Register(&Verb{
		Name:        "workspace_set_default",
		Description: "Flip the is_default flag onto the given workspace, clearing it from any prior default.",
		Invoke:      invokeWorkspaceSetDefault,
	})
}

func invokeWorkspaceCreate(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if workspaceStore == nil {
		return nil, fmt.Errorf("workspace_create: store not configured")
	}
	var req WorkspaceCreateRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("workspace_create: bad request: %w", err)
		}
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, fmt.Errorf("workspace_create: name required")
	}
	w, err := workspaceStore.Create(ctx, callerUserID(ctx), name, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	return json.Marshal(w)
}

func invokeWorkspaceList(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	if workspaceStore == nil {
		return nil, fmt.Errorf("workspace_list: store not configured")
	}
	ws, err := workspaceStore.List(ctx)
	if err != nil {
		return nil, err
	}
	if ws == nil {
		ws = []workspace.Workspace{}
	}
	return json.Marshal(WorkspaceListResponse{Workspaces: ws})
}

func invokeWorkspaceGet(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if workspaceStore == nil {
		return nil, fmt.Errorf("workspace_get: store not configured")
	}
	var req WorkspaceGetRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("workspace_get: bad request: %w", err)
		}
	}
	if strings.TrimSpace(req.ID) == "" {
		return nil, fmt.Errorf("workspace_get: id required")
	}
	w, err := workspaceStore.GetByID(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	return json.Marshal(w)
}

func invokeWorkspaceSetDefault(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if workspaceStore == nil {
		return nil, fmt.Errorf("workspace_set_default: store not configured")
	}
	var req WorkspaceGetRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("workspace_set_default: bad request: %w", err)
		}
	}
	if strings.TrimSpace(req.ID) == "" {
		return nil, fmt.Errorf("workspace_set_default: id required")
	}
	w, err := workspaceStore.SetDefault(ctx, req.ID, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	return json.Marshal(w)
}

// callerUserID extracts the authenticated user id from ctx. Returns ""
// when the verb is invoked by an unauthenticated caller (the CLI-local
// path until the multi-user gate lands).
func callerUserID(ctx context.Context) string {
	if u := auth.FromContext(ctx); u != nil {
		return u.ID
	}
	return ""
}
