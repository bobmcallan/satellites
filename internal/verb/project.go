// Project verbs — V5's per-engagement domain surface, bound to the
// workspace minted in PR 1.
//
// workspace_id defaults to the system default workspace (see
// workspace.SeedDefault) when not supplied. owner_user_id falls back
// to auth.FromContext(ctx) the same way workspace verbs do.

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
	Projects []project.Project `json:"projects"`
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
		// Fall back to the default workspace. Same boot-seeded singleton
		// that workspace_list returns when nothing else has been minted.
		if workspaceStore == nil {
			return nil, fmt.Errorf("project_create: workspace_id required (workspace store not configured)")
		}
		def, err := workspaceStore.GetDefault(ctx)
		if err != nil {
			return nil, fmt.Errorf("project_create: default workspace lookup: %w", err)
		}
		wsID = def.ID
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
	ps, err := projectStore.ListByWorkspace(ctx, strings.TrimSpace(req.WorkspaceID))
	if err != nil {
		return nil, err
	}
	if ps == nil {
		ps = []project.Project{}
	}
	return json.Marshal(ProjectListResponse{Projects: ps})
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
	p, err := projectStore.GetByID(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	return json.Marshal(p)
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

// Compile-time reference to keep workspace import meaningful even if
// the optional workspace_id default path is later refactored out.
var _ = workspace.StatusActive
