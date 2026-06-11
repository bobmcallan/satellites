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

type InvitationRedeemRequest struct {
	Token string `json:"token"`
}

// InvitationCreateResponse carries the stored invitation plus, for a link
// invite, the relative path a recipient redeems it at.
type InvitationCreateResponse struct {
	invitation.Invitation
	RedeemPath string `json:"redeem_path,omitempty"`
}

// redeemPath is the relative URL a link invite is redeemed at.
func redeemPath(token string) string { return "/invite/" + token }

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
	Register(&Verb{
		Name:        "invitation_redeem",
		Description: "Redeem an invite-link token as the authenticated caller → project/workspace membership.",
		Invoke:      invokeInvitationRedeem,
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

// alreadyMember reports whether userID already has effective membership of the
// invite target: an explicit member row, or — for a project — a workspace
// owner/admin who is implicitly a project admin. Used to make re-inviting an
// existing member a no-op.
func alreadyMember(ctx context.Context, scope, wsID, pjID, userID string) bool {
	if userID == "" {
		return false
	}
	switch scope {
	case invitation.ScopeWorkspace:
		if workspaceStore != nil && wsID != "" {
			if _, err := workspaceStore.GetRole(ctx, wsID, userID); err == nil {
				return true
			}
		}
	case invitation.ScopeProject:
		if projectStore != nil && pjID != "" {
			if _, err := projectStore.GetRole(ctx, pjID, userID); err == nil {
				return true
			}
		}
		if workspaceStore != nil && wsID != "" {
			if role, err := workspaceStore.GetRole(ctx, wsID, userID); err == nil &&
				(role == workspace.RoleOwner || role == workspace.RoleAdmin) {
				return true
			}
		}
	}
	return false
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

	// Link mode: no email → generate a stored, redeemable invite token instead
	// of an email-bound invite. Whoever redeems the token becomes a member.
	if strings.TrimSpace(req.Email) == "" {
		inv, err := invitationStore.CreateLink(ctx, invitation.CreateLinkInput{
			Scope:       req.Scope,
			WorkspaceID: wsID,
			ProjectID:   pjID,
			Role:        req.Role,
			InvitedBy:   callerUserID(ctx),
		}, time.Now().UTC())
		if err != nil {
			if errors.Is(err, invitation.ErrInvalidRole) {
				return nil, fmt.Errorf("invitation_create: invalid_role for scope %q", req.Scope)
			}
			return nil, err
		}
		return json.Marshal(InvitationCreateResponse{Invitation: inv, RedeemPath: redeemPath(inv.Token)})
	}

	// Resolve the invitee up front: it drives both the dedup short-circuit and
	// the immediate claim, and avoids a second lookup.
	var invitee *auth.User
	if authStore != nil {
		if u, err := authStore.GetUserByEmail(ctx, req.Email); err == nil && u != nil {
			invitee = u
		}
	}

	// Dedup (sty_1c266e21): if the invitee already has effective access to the
	// target (explicit member, or a workspace owner/admin who is implicitly a
	// project admin), inviting is a no-op with a clear signal — no new row.
	if invitee != nil && alreadyMember(ctx, req.Scope, wsID, pjID, invitee.ID) {
		return json.Marshal(map[string]string{"status": "already_member", "email": invitee.Email})
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
	if invitee != nil {
		if _, err := invitationStore.ClaimForEmail(ctx, inv.Email, invitee.ID, time.Now().UTC()); err != nil {
			return nil, fmt.Errorf("invitation_create: immediate claim: %w", err)
		}
		inv.Status = invitation.StatusAccepted
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

// invokeInvitationRedeem turns a held invite-link token into membership for the
// authenticated caller. Authorization is possession of the token — no
// canInvite gate — but the caller must be signed in.
func invokeInvitationRedeem(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if invitationStore == nil {
		return nil, fmt.Errorf("invitation_redeem: store not configured")
	}
	var req InvitationRedeemRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("invitation_redeem: bad request: %w", err)
		}
	}
	if strings.TrimSpace(req.Token) == "" {
		return nil, fmt.Errorf("invitation_redeem: token required")
	}
	caller := callerUserID(ctx)
	if caller == "" {
		return nil, fmt.Errorf("invitation_redeem: forbidden (sign in to redeem)")
	}
	inv, err := invitationStore.RedeemToken(ctx, req.Token, caller, time.Now().UTC())
	if err != nil {
		switch {
		case errors.Is(err, invitation.ErrNotFound):
			return nil, fmt.Errorf("invitation_redeem: not_found")
		case errors.Is(err, invitation.ErrNotPending):
			return nil, fmt.Errorf("invitation_redeem: already_redeemed")
		case errors.Is(err, invitation.ErrExpired):
			return nil, fmt.Errorf("invitation_redeem: expired")
		}
		return nil, err
	}
	return json.Marshal(inv)
}
