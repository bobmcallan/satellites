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
}

type ProjectGetRequest struct {
	ID string `json:"id"`
}

type ProjectUpdateRequest struct {
	ID          string  `json:"id"`
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	GitURL      *string `json:"git_url,omitempty"`
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
	return json.Marshal(ProjectListResponse{Projects: ps, Principles: principles})
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
	p, err := projectStore.Update(ctx, req.ID, project.UpdateInput{
		Name:        req.Name,
		Description: req.Description,
		GitURL:      req.GitURL,
	}, time.Now().UTC())
	if err != nil {
		if errors.Is(err, project.ErrInvalidGitRemote) {
			return nil, fmt.Errorf("project_update: git_url_invalid: %w", err)
		}
		return nil, err
	}
	return json.Marshal(p)
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

// Compile-time reference to keep workspace import meaningful even if
// the optional workspace_id default path is later refactored out.
var _ = workspace.StatusActive
