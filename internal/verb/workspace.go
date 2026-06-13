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
// package. Called by cmd/satellites-server on boot, after migrations.
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

// WorkspaceUpsertRequest is the input to workspace_upsert. Mode is decided by
// inspection on ID (document_upsert convention): empty ID + Name → create;
// ID present → patch the supplied fields. Name/Description are pointers so a
// patch distinguishes "not supplied" (nil → untouched) from an explicit value.
// Ownership/created_by is never a request field — it derives from the authed
// identity on create and is immutable thereafter.
type WorkspaceUpsertRequest struct {
	ID          string  `json:"id,omitempty"`
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

// WorkspaceArchiveRequest is the input to workspace_archive. restore defaults
// to false (soft-archive: status→archived); restore=true un-archives. Archive
// is reversible by design — there is deliberately no hard-delete verb on the
// MCP surface (destructive delete stays admin-UI-only).
type WorkspaceArchiveRequest struct {
	WorkspaceID string `json:"workspace_id"`
	Restore     bool   `json:"restore,omitempty"`
}

func init() {
	Register(&Verb{
		Name:        "workspace_create",
		Description: "Create a new workspace.",
		Invoke:      invokeWorkspaceCreate,
	})
	Register(&Verb{
		Name:        "workspace_upsert",
		Description: "Create a workspace (no id) or patch its name/description (id present). Ownership derives from the authed identity; create requires a human platform-admin (no api-key), patch requires the owner or a platform-admin.",
		Invoke:      invokeWorkspaceUpsert,
		MCPRole:     MCPRoleWorkspaceAdmin,
	})
	Register(&Verb{
		Name:        "workspace_archive",
		Description: "Reversibly soft-archive a workspace (status→archived), or restore it with restore=true. Archived workspaces drop from normal list/read paths but the record and its data are retained — there is no hard-delete on the MCP surface. Requires the workspace owner or a platform-admin. The default workspace cannot be archived, nor one that still holds home (writable) projects (move them first).",
		Invoke:      invokeWorkspaceArchive,
		MCPRole:     MCPRoleWorkspaceAdmin,
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
	Register(&Verb{
		Name:        "workspace_personal",
		Description: "Return the caller's personal workspace (the deterministic \"my workspace\").",
		Invoke:      invokeWorkspacePersonal,
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

// invokeWorkspaceUpsert is the single create+patch surface for workspaces,
// shaped like document_upsert (mode by inspection on id). The role tier gate
// (MCPRoleWorkspaceAdmin) only controls visibility/dispatch on the MCP surface;
// the per-mode authz below is the authority.
func invokeWorkspaceUpsert(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if workspaceStore == nil {
		return nil, fmt.Errorf("workspace_upsert: store not configured")
	}
	var req WorkspaceUpsertRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("workspace_upsert: bad request: %w", err)
		}
	}
	now := time.Now().UTC()
	id := strings.TrimSpace(req.ID)

	// CREATE — no id supplied.
	if id == "" {
		// Ownership binds to a human OAuth identity. An executor/agent api-key
		// is refused so no workspace is owned by an ephemeral token
		// (orphaned-admin guard). CLI-local (no authStore) is exempt.
		if authStore != nil {
			if auth.APIKeyRoleFromContext(ctx) != "" {
				return nil, fmt.Errorf("workspace_upsert: %w: workspace creation requires a human identity, not an api-key", ErrForbidden)
			}
			if !callerIsGlobalAdmin(ctx) {
				return nil, fmt.Errorf("workspace_upsert: %w: workspace creation requires platform-admin", ErrForbidden)
			}
		}
		if req.Name == nil || strings.TrimSpace(*req.Name) == "" {
			return nil, fmt.Errorf("workspace_upsert: %w: name required to create", ErrBadRequest)
		}
		owner := callerUserID(ctx)
		w, err := workspaceStore.Create(ctx, owner, strings.TrimSpace(*req.Name), now)
		if err != nil {
			return nil, err
		}
		// Apply a supplied description in the same call (create + set).
		if req.Description != nil {
			w, err = workspaceStore.Update(ctx, w.ID, nil, req.Description, now)
			if err != nil {
				return nil, err
			}
		}
		return json.Marshal(w)
	}

	// PATCH — id present. Authz: platform-admin OR the workspace owner.
	ws, err := workspaceStore.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if authStore != nil && !canPatchWorkspace(ctx, ws) {
		return nil, fmt.Errorf("workspace_upsert: %w: not owner or platform-admin of workspace %s", ErrForbidden, id)
	}
	w, err := workspaceStore.Update(ctx, id, req.Name, req.Description, now)
	if err != nil {
		return nil, err
	}
	return json.Marshal(w)
}

// invokeWorkspaceArchive reversibly soft-archives a workspace (or restores it).
// Authz mirrors workspace_upsert patch — owner OR platform-admin; the
// MCPRoleWorkspaceAdmin tier only gates visibility/dispatch on the MCP surface.
// There is deliberately no hard-delete: archive is a status flip that retains
// the record and its data (sty_f5c08ea0).
func invokeWorkspaceArchive(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if workspaceStore == nil {
		return nil, fmt.Errorf("workspace_archive: store not configured")
	}
	var req WorkspaceArchiveRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("workspace_archive: bad request: %w", err)
		}
	}
	id := strings.TrimSpace(req.WorkspaceID)
	if id == "" {
		return nil, fmt.Errorf("workspace_archive: %w: workspace_id required", ErrBadRequest)
	}
	now := time.Now().UTC()

	ws, err := workspaceStore.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if authStore != nil && !canPatchWorkspace(ctx, ws) {
		return nil, fmt.Errorf("workspace_archive: %w: not owner or platform-admin of workspace %s", ErrForbidden, id)
	}

	// RESTORE — un-archive (idempotent if already active). No guardrails:
	// restoring never orphans state.
	if req.Restore {
		if ws.Status == workspace.StatusActive {
			return json.Marshal(ws)
		}
		w, err := workspaceStore.SetStatus(ctx, id, workspace.StatusActive, now)
		if err != nil {
			return nil, err
		}
		return json.Marshal(w)
	}

	// ARCHIVE — idempotent no-op if already archived.
	if ws.Status == workspace.StatusArchived {
		return json.Marshal(ws)
	}
	// Guardrail: the default workspace anchors the system and cannot be archived.
	if ws.IsDefault {
		return nil, fmt.Errorf("workspace_archive: %w: the default workspace cannot be archived", ErrBadRequest)
	}
	// Guardrail: no home (writable) project may be left with an archived home —
	// move them to another workspace first (project home-move).
	if projectStore != nil {
		homed, err := projectStore.ListByWorkspace(ctx, id)
		if err != nil {
			return nil, err
		}
		if len(homed) > 0 {
			return nil, fmt.Errorf("workspace_archive: %w: workspace %s still holds %d home project(s); move them to another workspace before archiving", ErrBadRequest, id, len(homed))
		}
	}
	// Clear any readonly mounts INTO this workspace so no live mount points at
	// an archived workspace (the mount is only a readonly view; dropping it
	// loses no home data).
	mounts, err := workspaceStore.ListMounts(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, m := range mounts {
		if err := workspaceStore.RemoveMount(ctx, id, m.ProjectID); err != nil {
			return nil, err
		}
	}
	w, err := workspaceStore.SetStatus(ctx, id, workspace.StatusArchived, now)
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
	// Identity-aware (epic:user-admin, sty_14cf17e3): the all-workspaces listing
	// is global-admin-gated. A non-admin authenticated caller sees only the
	// workspaces they belong to; CLI-local (no authStore) keeps the full list.
	if authStore != nil && !callerIsGlobalAdmin(ctx) {
		caller := callerUserID(ctx)
		mine := map[string]bool{}
		if caller != "" {
			if ids, err := workspaceStore.ListWorkspaceIDsForUser(ctx, caller); err == nil {
				for _, id := range ids {
					mine[id] = true
				}
			}
		}
		// Archived workspaces drop from normal list paths; a global platform-admin
		// keeps visibility (recoverability safety net), so this filter is scoped
		// to non-admin callers alongside the membership filter (sty_f5c08ea0).
		filtered := ws[:0]
		for _, w := range ws {
			if mine[w.ID] && w.Status != workspace.StatusArchived {
				filtered = append(filtered, w)
			}
		}
		ws = filtered
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
	// An archived workspace is gone from normal read paths: a non-admin caller
	// gets ErrNotFound (the portal detail handler maps this to 404), while a
	// global platform-admin still reads it for recovery (sty_f5c08ea0).
	if authStore != nil && !callerIsGlobalAdmin(ctx) && w.Status == workspace.StatusArchived {
		return nil, workspace.ErrNotFound
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

func invokeWorkspacePersonal(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	if workspaceStore == nil {
		return nil, fmt.Errorf("workspace_personal: store not configured")
	}
	uid := callerUserID(ctx)
	if uid == "" {
		return nil, fmt.Errorf("workspace_personal: no caller identity")
	}
	w, err := workspaceStore.GetPersonalForUser(ctx, uid)
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
