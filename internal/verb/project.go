// Project verbs — V5's per-engagement domain surface, bound to the
// workspace minted in PR 1.
//
// workspace_id defaults to the caller's personal workspace
// (epic:user-admin — the shared default was retired) when not supplied.
// owner_user_id falls back to auth.FromContext(ctx) the same way
// workspace verbs do.

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

var projectStore *project.Store

// SetProjectStore wires the server's project.Store into the verb
// package. Called from cmd/satellites-server on boot.
func SetProjectStore(s *project.Store) { projectStore = s }

type ProjectCreateRequest struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	GitURL      string `json:"git_url,omitempty"`
}

type ProjectListRequest struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
}

type ProjectListResponse struct {
	Projects   []project.Project `json:"projects"`
	Principles []Principle       `json:"principles,omitempty"`
	// Roles maps project_id → the caller's effective role (admin|write|read)
	// on that project, resolved through effectiveProjectRole. Populated only
	// when an auth store is wired (the server path); empty for CLI-local
	// callers, who bypass role resolution. Additive — JSON consumers that
	// only read Projects are unaffected. Consumed by the workspace detail page
	// to show per-repo access (sty_67a66574).
	Roles map[string]string `json:"roles,omitempty"`
}

type ProjectGetRequest struct {
	ID string `json:"id"`
}

type ProjectUpdateRequest struct {
	ID          string  `json:"id"`
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	GitURL      *string `json:"git_url,omitempty"`
	// WorkspaceID, when supplied and different from the project's current home,
	// re-homes the project's single writable workspace (sty_896cebb1). This is
	// the patch-convention expression of a home move — it carries a stricter,
	// two-sided admin authz than the ordinary field patch (see invokeProjectUpdate).
	WorkspaceID *string `json:"workspace_id,omitempty"`
}

type ProjectMatchRequest struct {
	GitURL string `json:"git_url"`
}

type ProjectMatchResponse struct {
	ProjectID   string      `json:"project_id"`
	WorkspaceID string      `json:"workspace_id"`
	Name        string      `json:"name"`
	MatchedURL  string      `json:"matched_url"`
	Principles  []Principle `json:"principles,omitempty"`
}

// projectGetResponse is the wire shape for project_get. The base Project
// fields stay top-level (preserves the existing wire contract), with
// the principles sidecar attached as an optional field. Anonymous
// embedding keeps callers that only parse the project fields working.
type projectGetResponse struct {
	project.Project
	Principles []Principle `json:"principles,omitempty"`
}

func init() {
	Register(&Verb{
		Name:        "project_create",
		Description: "Create a new project in a workspace.",
		Invoke:      invokeProjectCreate,
	})
	Register(&Verb{
		Name:        "project_list",
		Description: "List projects, optionally filtered by workspace_id.",
		Invoke:      invokeProjectList,
	})
	Register(&Verb{
		Name:        "project_get",
		Description: "Fetch a project by id.",
		Invoke:      invokeProjectGet,
	})
	Register(&Verb{
		Name:        "project_update",
		Description: "Patch mutable fields on a project. workspace_id is immutable here.",
		Invoke:      invokeProjectUpdate,
	})
	Register(&Verb{
		Name:        "project_match",
		Description: "Resolve a project_id from any-form git remote URL. Bootstrap surface.",
		Invoke:      invokeProjectMatch,
	})
}

func invokeProjectCreate(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if projectStore == nil {
		return nil, fmt.Errorf("project_create: store not configured")
	}
	var req ProjectCreateRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("project_create: bad request: %w", err)
		}
	}
	if strings.TrimSpace(req.Name) == "" {
		return nil, fmt.Errorf("project_create: name required")
	}
	wsID := strings.TrimSpace(req.WorkspaceID)
	if wsID == "" {
		// Fall back to the caller's personal workspace (epic:user-admin —
		// the shared default was retired in sty_3a54bf42). Requires a
		// caller identity to resolve "my workspace".
		if workspaceStore == nil {
			return nil, fmt.Errorf("project_create: workspace_id required (workspace store not configured)")
		}
		uid := callerUserID(ctx)
		if uid == "" {
			return nil, fmt.Errorf("project_create: workspace_id required (no caller identity to resolve a personal workspace)")
		}
		pw, err := workspaceStore.GetPersonalForUser(ctx, uid)
		if err != nil {
			return nil, fmt.Errorf("project_create: personal workspace lookup: %w", err)
		}
		wsID = pw.ID
	}
	// Only a workspace owner/admin (or global admin) may create a project in
	// the target workspace (epic:user-admin, sty_c96cc77f). CLI-local callers
	// (no auth wiring) bypass.
	if authStore != nil && !canManageWorkspace(ctx, wsID) {
		return nil, fmt.Errorf("project_create: %w: not an admin of workspace %s", ErrForbidden, wsID)
	}
	p, err := projectStore.Create(ctx, project.CreateInput{
		WorkspaceID: wsID,
		Name:        req.Name,
		Description: req.Description,
		GitURL:      req.GitURL,
		OwnerUserID: callerUserID(ctx),
	}, time.Now().UTC())
	if err != nil {
		if errors.Is(err, project.ErrInvalidGitRemote) {
			return nil, fmt.Errorf("project_create: git_url_invalid: %w", err)
		}
		return nil, err
	}
	return json.Marshal(p)
}

func invokeProjectList(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if projectStore == nil {
		return nil, fmt.Errorf("project_list: store not configured")
	}
	var req ProjectListRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("project_list: bad request: %w", err)
		}
	}
	wsID := strings.TrimSpace(req.WorkspaceID)
	if authStore != nil && !canListWorkspaceProjects(ctx, wsID) {
		return nil, fmt.Errorf("project_list: %w: not authorized to list workspace %q projects", ErrForbidden, wsID)
	}
	ps, err := projectStore.ListByWorkspace(ctx, wsID)
	if err != nil {
		return nil, err
	}
	if ps == nil {
		ps = []project.Project{}
	}
	// Workspace is identified when the caller filtered by it; deliver
	// workspace principles. Project principles wait for a specific
	// project lookup (project_get / project_match).
	var principles []Principle
	if wsID != "" {
		principles = LoadPrinciples(ctx, PrincipleScopeRequest{Scope: PrincipleScopeWorkspace, WorkspaceID: wsID})
	}
	// Annotate each project with the caller's effective role so the workspace
	// detail page can show per-repo access (sty_67a66574). Server path only —
	// CLI-local callers (no authStore) skip role resolution and get no map.
	var roles map[string]string
	if authStore != nil {
		roles = make(map[string]string, len(ps))
		for _, p := range ps {
			if r := effectiveProjectRoleWS(ctx, wsID, p.ID); r != "" {
				roles[p.ID] = r
			}
		}
	}
	return json.Marshal(ProjectListResponse{Projects: ps, Principles: principles, Roles: roles})
}

func invokeProjectGet(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if projectStore == nil {
		return nil, fmt.Errorf("project_get: store not configured")
	}
	var req ProjectGetRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("project_get: bad request: %w", err)
		}
	}
	if strings.TrimSpace(req.ID) == "" {
		return nil, fmt.Errorf("project_get: id required")
	}
	if authStore != nil && !effectiveProjectRoleAtLeast(ctx, req.ID, project.RoleRead) {
		return nil, fmt.Errorf("project_get: %w: no read access to project %s", ErrForbidden, req.ID)
	}
	p, err := projectStore.GetByID(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	principles := LoadPrinciples(ctx,
		PrincipleScopeRequest{Scope: PrincipleScopeWorkspace, WorkspaceID: p.WorkspaceID},
		PrincipleScopeRequest{Scope: PrincipleScopeProject, WorkspaceID: p.WorkspaceID, ProjectID: p.ID},
	)
	return json.Marshal(projectGetResponse{Project: p, Principles: principles})
}

func invokeProjectUpdate(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if projectStore == nil {
		return nil, fmt.Errorf("project_update: store not configured")
	}
	var req ProjectUpdateRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("project_update: bad request: %w", err)
		}
	}
	if strings.TrimSpace(req.ID) == "" {
		return nil, fmt.Errorf("project_update: id required")
	}
	if authStore != nil && !effectiveProjectRoleAtLeast(ctx, req.ID, project.RoleAdmin) {
		return nil, fmt.Errorf("project_update: %w: project admin required for %s", ErrForbidden, req.ID)
	}
	now := time.Now().UTC()

	// Home move (sty_896cebb1): a supplied workspace_id that differs from the
	// project's current home re-points the single writable home. This carries a
	// stricter, two-sided admin authz than the ordinary field patch above.
	if req.WorkspaceID != nil {
		cur, err := projectStore.GetByID(ctx, req.ID)
		if err != nil {
			return nil, err
		}
		dest := strings.TrimSpace(*req.WorkspaceID)
		if dest == "" {
			return nil, fmt.Errorf("project_update: %w: workspace_id cannot be cleared", ErrBadRequest)
		}
		if dest != cur.WorkspaceID {
			if err := moveProjectHome(ctx, cur, dest, now); err != nil {
				return nil, err
			}
		}
	}

	p, err := projectStore.Update(ctx, req.ID, project.UpdateInput{
		Name:        req.Name,
		Description: req.Description,
		GitURL:      req.GitURL,
	}, now)
	if err != nil {
		if errors.Is(err, project.ErrInvalidGitRemote) {
			return nil, fmt.Errorf("project_update: git_url_invalid: %w", err)
		}
		return nil, err
	}
	return json.Marshal(p)
}

// moveProjectHome re-homes p to destWsID, enforcing the home-move invariants
// (sty_896cebb1). Authz is two-sided: the caller must be able to administer
// BOTH the source and the destination workspace (or be a platform-admin); the
// destination must exist and must not already readonly-mount the project (no
// workspace is simultaneously mount-readonly and home-writable). Readonly
// mounts into OTHER workspaces are untouched, and project grants re-evaluate
// against the new home by construction (effectiveProjectRole reads the project's
// current workspace_id). CLI-local callers (no authStore) bypass the authz.
func moveProjectHome(ctx context.Context, p project.Project, destWsID string, now time.Time) error {
	if workspaceStore == nil {
		return fmt.Errorf("project_update: workspace store not configured")
	}
	if authStore != nil {
		if !canManageWorkspace(ctx, p.WorkspaceID) || !canManageWorkspace(ctx, destWsID) {
			return fmt.Errorf("project_update: %w: home move requires admin on both the source and destination workspace", ErrForbidden)
		}
	}
	if _, err := workspaceStore.GetByID(ctx, destWsID); err != nil {
		if errors.Is(err, workspace.ErrNotFound) {
			return fmt.Errorf("project_update: %w: destination workspace %s does not exist", ErrNotFound, destWsID)
		}
		return err
	}
	mounted, err := workspaceStore.MountExists(ctx, destWsID, p.ID)
	if err != nil {
		return err
	}
	if mounted {
		return fmt.Errorf("project_update: %w: destination workspace %s already readonly-mounts this project; remove the mount before re-homing", ErrBadRequest, destWsID)
	}
	if _, err := projectStore.SetWorkspace(ctx, p.ID, destWsID, now); err != nil {
		return err
	}
	return nil
}

func invokeProjectMatch(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if projectStore == nil {
		return nil, fmt.Errorf("project_match: store not configured")
	}
	var req ProjectMatchRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("project_match: bad request: %w", err)
		}
	}
	if strings.TrimSpace(req.GitURL) == "" {
		return nil, fmt.Errorf("project_match: git_url required")
	}
	p, err := projectStore.GetByGitURL(ctx, req.GitURL)
	if err != nil {
		if errors.Is(err, project.ErrInvalidGitRemote) {
			return nil, fmt.Errorf("project_match: git_url_invalid: %w", err)
		}
		if errors.Is(err, project.ErrNotFound) {
			return nil, fmt.Errorf("project_match: not_found")
		}
		return nil, err
	}
	principles := LoadPrinciples(ctx,
		PrincipleScopeRequest{Scope: PrincipleScopeWorkspace, WorkspaceID: p.WorkspaceID},
		PrincipleScopeRequest{Scope: PrincipleScopeProject, WorkspaceID: p.WorkspaceID, ProjectID: p.ID},
	)
	return json.Marshal(ProjectMatchResponse{
		ProjectID:   p.ID,
		WorkspaceID: p.WorkspaceID,
		Name:        p.Name,
		MatchedURL:  p.GitURLCanonical,
		Principles:  principles,
	})
}
