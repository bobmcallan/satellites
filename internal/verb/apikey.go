// API-key verbs — in-band token minting for MCP-only agents.
//
// `auth_bootstrap.kind: auth_login` in the install schema required a
// shell-side `satellites auth login` flow. MCP-only callers (Claude
// web, etc.) can't shell out, so the load-context's Step 3 used to
// stall waiting for an out-of-band token paste. These verbs close the
// loop: an MCP-authenticated session calls apikey_create to mint a
// project-scoped token, writes it into `satellites.toml`, and proceeds
// without operator intervention.
//
// The minted key inherits the caller's identity (api_keys.user_id =
// caller). Membership on the target workspace is verified before
// minting; project_id must belong to that workspace.

package verb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/workspace"
)

// APIKeyCreateRequest is the input to apikey_create.
//
// Scopes are accepted on the request shape per the contract but not
// persisted yet — the api_keys table has no scope column, and
// scope-enforcement is a separate workstream. Callers that supply
// scopes get a round-tripped key today; tightening lands when the
// substrate grows the column.
type APIKeyCreateRequest struct {
	WorkspaceID string   `json:"workspace_id,omitempty"`
	ProjectID   string   `json:"project_id,omitempty"`
	AgentName   string   `json:"agent_name,omitempty"`
	Scopes      []string `json:"scopes,omitempty"`
}

// APIKeyCreateResponse mirrors the shape declared in story
// sty_3b22918c AC#1.
type APIKeyCreateResponse struct {
	APIKey      string    `json:"apikey"`
	KeyID       string    `json:"key_id"`
	WorkspaceID string    `json:"workspace_id,omitempty"`
	ProjectID   string    `json:"project_id,omitempty"`
	AgentName   string    `json:"agent_name,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// APIKeyListRequest filters by any combination of workspace, project,
// or agent_name. All filters are AND. An empty request returns every
// non-revoked key owned by the caller.
type APIKeyListRequest struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
	AgentName   string `json:"agent_name,omitempty"`
}

// APIKeyRow is the redacted shape returned by apikey_list — never
// includes the raw key (only visible at mint time).
type APIKeyRow struct {
	KeyID       string    `json:"key_id"`
	WorkspaceID string    `json:"workspace_id,omitempty"`
	ProjectID   string    `json:"project_id,omitempty"`
	AgentName   string    `json:"agent_name,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// APIKeyListResponse wraps the list result.
type APIKeyListResponse struct {
	Keys []APIKeyRow `json:"keys"`
}

// APIKeyRevokeRequest is the input to apikey_revoke.
type APIKeyRevokeRequest struct {
	KeyID string `json:"key_id"`
}

func init() {
	Register(&Verb{
		Name:        "apikey_create",
		Description: "Mint a project-scoped api-key for the authenticated caller. Bootstrap surface for MCP-only agents.",
		Invoke:      invokeAPIKeyCreate,
	})
	Register(&Verb{
		Name:        "apikey_list",
		Description: "List the authenticated caller's non-revoked api-keys, optionally filtered by workspace_id / project_id / agent_name.",
		Invoke:      invokeAPIKeyList,
	})
	Register(&Verb{
		Name:        "apikey_revoke",
		Description: "Revoke one of the authenticated caller's api-keys by key_id.",
		Invoke:      invokeAPIKeyRevoke,
	})
}

func invokeAPIKeyCreate(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if authStore == nil {
		return nil, fmt.Errorf("apikey_create: auth store not configured")
	}
	caller := auth.FromContext(ctx)
	if caller == nil {
		return nil, fmt.Errorf("apikey_create: unauthenticated — bearer credential required")
	}

	var req APIKeyCreateRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("apikey_create: bad request: %w", err)
		}
	}

	projectID := strings.TrimSpace(req.ProjectID)
	workspaceID := strings.TrimSpace(req.WorkspaceID)
	if projectID == "" && workspaceID == "" {
		return nil, fmt.Errorf("apikey_create: workspace_id or project_id required")
	}

	if projectID != "" {
		if projectStore == nil {
			return nil, fmt.Errorf("apikey_create: project store not configured")
		}
		p, err := projectStore.GetByID(ctx, projectID)
		if err != nil {
			if errors.Is(err, project.ErrNotFound) {
				return nil, fmt.Errorf("apikey_create: project_not_found")
			}
			return nil, err
		}
		if workspaceID != "" && workspaceID != p.WorkspaceID {
			return nil, fmt.Errorf("apikey_create: project_id %q is in workspace %q, not %q", projectID, p.WorkspaceID, workspaceID)
		}
		workspaceID = p.WorkspaceID
	}

	if workspaceStore == nil {
		return nil, fmt.Errorf("apikey_create: workspace store not configured")
	}
	if _, err := workspaceStore.GetRole(ctx, workspaceID, caller.ID); err != nil {
		if errors.Is(err, workspace.ErrMemberNotFound) {
			return nil, fmt.Errorf("apikey_create: forbidden — caller is not a member of workspace %q", workspaceID)
		}
		return nil, err
	}

	agentName := strings.TrimSpace(req.AgentName)
	keyID := fmt.Sprintf("apk_mcp_%s", randomKeyIDSuffix())
	rawKey, k, err := authStore.IssueAPIKey(ctx, keyID, caller.ID, projectID, agentName)
	if err != nil {
		return nil, fmt.Errorf("apikey_create: issue: %w", err)
	}

	return json.Marshal(APIKeyCreateResponse{
		APIKey:      rawKey,
		KeyID:       k.ID,
		WorkspaceID: workspaceID,
		ProjectID:   k.ProjectID,
		AgentName:   k.AgentName,
		CreatedAt:   k.CreatedAt,
	})
}

func invokeAPIKeyList(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if authStore == nil {
		return nil, fmt.Errorf("apikey_list: auth store not configured")
	}
	caller := auth.FromContext(ctx)
	if caller == nil {
		return nil, fmt.Errorf("apikey_list: unauthenticated — bearer credential required")
	}

	var req APIKeyListRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("apikey_list: bad request: %w", err)
		}
	}
	workspaceFilter := strings.TrimSpace(req.WorkspaceID)
	projectFilter := strings.TrimSpace(req.ProjectID)
	agentFilter := strings.TrimSpace(req.AgentName)

	keys, err := authStore.ListAPIKeysForUser(ctx, caller.ID)
	if err != nil {
		return nil, err
	}

	out := make([]APIKeyRow, 0, len(keys))
	for _, k := range keys {
		if projectFilter != "" && k.ProjectID != projectFilter {
			continue
		}
		if agentFilter != "" && k.AgentName != agentFilter {
			continue
		}
		row := APIKeyRow{
			KeyID:     k.ID,
			ProjectID: k.ProjectID,
			AgentName: k.AgentName,
			CreatedAt: k.CreatedAt,
		}
		if workspaceFilter != "" || k.ProjectID != "" {
			ws, err := workspaceIDForProject(ctx, k.ProjectID)
			if err != nil {
				return nil, err
			}
			row.WorkspaceID = ws
			if workspaceFilter != "" && ws != workspaceFilter {
				continue
			}
		}
		out = append(out, row)
	}
	return json.Marshal(APIKeyListResponse{Keys: out})
}

func invokeAPIKeyRevoke(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if authStore == nil {
		return nil, fmt.Errorf("apikey_revoke: auth store not configured")
	}
	caller := auth.FromContext(ctx)
	if caller == nil {
		return nil, fmt.Errorf("apikey_revoke: unauthenticated — bearer credential required")
	}

	var req APIKeyRevokeRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("apikey_revoke: bad request: %w", err)
		}
	}
	keyID := strings.TrimSpace(req.KeyID)
	if keyID == "" {
		return nil, fmt.Errorf("apikey_revoke: key_id required")
	}

	if err := authStore.RevokeKeyForUser(ctx, caller.ID, keyID); err != nil {
		return nil, fmt.Errorf("apikey_revoke: %w", err)
	}
	return json.Marshal(map[string]string{"key_id": keyID, "revoked": "true"})
}

// workspaceIDForProject is a tiny lookup: empty projectID → empty
// workspace (system-scope or unscoped keys). Used by apikey_list to
// project workspace_id into the response row.
func workspaceIDForProject(ctx context.Context, projectID string) (string, error) {
	if projectID == "" {
		return "", nil
	}
	if projectStore == nil {
		return "", nil
	}
	p, err := projectStore.GetByID(ctx, projectID)
	if err != nil {
		if errors.Is(err, project.ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	return p.WorkspaceID, nil
}

func randomKeyIDSuffix() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
