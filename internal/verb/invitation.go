// Invitation verbs — epic:user-admin (sty_0e88352a). Create / list / revoke
// pending email invitations, authorization-gated by canInvite. CLI + portal
// handlers only; kept off the MCP surface per no-new-mcp-verbs.

package verb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/invitation"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/workspace"
)

var invitationStore *invitation.Store

// SetInvitationStore wires the server's invitation.Store into the verb
// package. Called by cmd/satellites-server on boot.
func SetInvitationStore(s *invitation.Store) { invitationStore = s }

type InvitationCreateRequest struct {
	Email       string `json:"email"`
	Scope       string `json:"scope"`
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id"`
	Role        string `json:"role"`
}

type InvitationListRequest struct {
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id"`
	Status      string `json:"status"`
}

type InvitationListResponse struct {
	Invitations []invitation.Invitation `json:"invitations"`
}

type InvitationRevokeRequest struct {
	ID string `json:"id"`
}

func init() {
	Register(&Verb{
		Name:        "invitation_create",
		Description: "Invite an email to a workspace or project at a role (authorization-gated).",
		Invoke:      invokeInvitationCreate,
	})
	Register(&Verb{
		Name:        "invitation_list",
		Description: "List invitations for a workspace or project.",
		Invoke:      invokeInvitationList,
	})
	Register(&Verb{
		Name:        "invitation_revoke",
		Description: "Revoke a pending invitation by id.",
		Invoke:      invokeInvitationRevoke,
	})
}

// canInvite reports whether the caller may manage invitations for the given
// target: a global admin anywhere; a workspace owner/admin for that workspace
// and its projects; a project admin for that project. Returns a non-nil error
// only on a store failure (treat as deny).
func canInvite(ctx context.Context, scope, workspaceID, projectID string) (bool, error) {
	caller := callerUserID(ctx)
	if caller == "" {
		return false, nil
	}
	// Global admin → allowed anywhere.
	if authStore != nil {
		if u, err := authStore.GetUserByID(ctx, caller); err == nil && u != nil && u.Role == auth.RoleAdmin {
			return true, nil
		}
	}
	if workspaceStore == nil {
		return false, nil
	}
	wsAdmin := func(wsID string) bool {
		if wsID == "" {
			return false
		}
		role, err := workspaceStore.GetRole(ctx, wsID, caller)
		return err == nil && (role == workspace.RoleOwner || role == workspace.RoleAdmin)
	}
	switch scope {
	case invitation.ScopeWorkspace:
		return wsAdmin(workspaceID), nil
	case invitation.ScopeProject:
		// Workspace owner/admin of the project's workspace may invite to it.
		if projectStore != nil && projectID != "" {
			if pj, err := projectStore.GetByID(ctx, projectID); err == nil && wsAdmin(pj.WorkspaceID) {
				return true, nil
			}
			// Or an explicit project admin.
			if role, err := projectStore.GetRole(ctx, projectID, caller); err == nil && role == project.RoleAdmin {
				return true, nil
			}
		}
		return false, nil
	}
	return false, nil
}

func invokeInvitationCreate(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if invitationStore == nil {
		return nil, fmt.Errorf("invitation_create: store not configured")
	}
	var req InvitationCreateRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("invitation_create: bad request: %w", err)
		}
	}
	if !invitation.IsValidScope(req.Scope) {
		return nil, fmt.Errorf("invitation_create: invalid scope (want workspace|project)")
	}

	// For project scope, resolve the project's workspace for audit/listing.
	wsID := strings.TrimSpace(req.WorkspaceID)
	pjID := strings.TrimSpace(req.ProjectID)
	if req.Scope == invitation.ScopeProject {
		if pjID == "" {
			return nil, fmt.Errorf("invitation_create: project_id required for project scope")
		}
		if projectStore != nil {
			if pj, err := projectStore.GetByID(ctx, pjID); err == nil {
				wsID = pj.WorkspaceID
			}
		}
	}

	ok, err := canInvite(ctx, req.Scope, wsID, pjID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("invitation_create: forbidden")
	}

	inv, err := invitationStore.Create(ctx, invitation.CreateInput{
		Email:       req.Email,
		Scope:       req.Scope,
		WorkspaceID: wsID,
		ProjectID:   pjID,
		Role:        req.Role,
		InvitedBy:   callerUserID(ctx),
	}, time.Now().UTC())
	if err != nil {
		switch {
		case errors.Is(err, invitation.ErrDuplicate):
			return nil, fmt.Errorf("invitation_create: duplicate (a pending invite for that email+target exists)")
		case errors.Is(err, invitation.ErrInvalidRole):
			return nil, fmt.Errorf("invitation_create: invalid_role for scope %q", req.Scope)
		case errors.Is(err, invitation.ErrInvalidScope):
			return nil, fmt.Errorf("invitation_create: invalid scope")
		}
		return nil, err
	}

	// AC#4: if the invitee already has an account, claim immediately so they
	// get access now (the invitation row stays for audit, marked accepted).
	if authStore != nil {
		if u, err := authStore.GetUserByEmail(ctx, inv.Email); err == nil && u != nil {
			if _, err := invitationStore.ClaimForEmail(ctx, inv.Email, u.ID, time.Now().UTC()); err != nil {
				return nil, fmt.Errorf("invitation_create: immediate claim: %w", err)
			}
			inv.Status = invitation.StatusAccepted
		}
	}
	return json.Marshal(inv)
}

func invokeInvitationList(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if invitationStore == nil {
		return nil, fmt.Errorf("invitation_list: store not configured")
	}
	var req InvitationListRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("invitation_list: bad request: %w", err)
		}
	}
	wsID := strings.TrimSpace(req.WorkspaceID)
	pjID := strings.TrimSpace(req.ProjectID)
	if wsID == "" && pjID == "" {
		return nil, fmt.Errorf("invitation_list: workspace_id or project_id required")
	}
	scope := invitation.ScopeWorkspace
	if pjID != "" {
		scope = invitation.ScopeProject
	}
	ok, err := canInvite(ctx, scope, wsID, pjID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("invitation_list: forbidden")
	}
	invs, err := invitationStore.List(ctx, invitation.ListInput{
		WorkspaceID: wsID, ProjectID: pjID, Status: strings.TrimSpace(req.Status),
	})
	if err != nil {
		return nil, err
	}
	if invs == nil {
		invs = []invitation.Invitation{}
	}
	return json.Marshal(InvitationListResponse{Invitations: invs})
}

func invokeInvitationRevoke(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if invitationStore == nil {
		return nil, fmt.Errorf("invitation_revoke: store not configured")
	}
	var req InvitationRevokeRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("invitation_revoke: bad request: %w", err)
		}
	}
	if strings.TrimSpace(req.ID) == "" {
		return nil, fmt.Errorf("invitation_revoke: id required")
	}
	inv, err := invitationStore.GetByID(ctx, req.ID)
	if err != nil {
		if errors.Is(err, invitation.ErrNotFound) {
			return nil, fmt.Errorf("invitation_revoke: not_found")
		}
		return nil, err
	}
	ok, err := canInvite(ctx, inv.Scope, inv.WorkspaceID, inv.ProjectID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("invitation_revoke: forbidden")
	}
	if err := invitationStore.Revoke(ctx, req.ID, time.Now().UTC()); err != nil {
		if errors.Is(err, invitation.ErrNotPending) {
			return nil, fmt.Errorf("invitation_revoke: not_pending")
		}
		return nil, err
	}
	return json.Marshal(map[string]string{"id": req.ID, "status": invitation.StatusRevoked})
}
