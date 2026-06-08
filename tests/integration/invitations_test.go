//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/invitation"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestInvitationStore covers the store-level invite lifecycle (sty_0e88352a):
// create (dedup), claim-on-login for both scopes (case-insensitive),
// revoke-before-claim.
func TestInvitationStore(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	ctx := context.Background()
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	authStore := auth.New(env.DB)
	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	invStore := invitation.New(env.DB)

	owner, _ := authStore.CreateUser(ctx, "usr_inv_owner", "owner@inv.local", "Owner", auth.RoleAdmin)
	ws, _ := wsStore.Create(ctx, owner.ID, "inv-ws", now)
	pj, _ := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "inv-pj"}, now)

	t.Run("workspace invite + dedup + case-insensitive claim", func(t *testing.T) {
		inv, err := invStore.Create(ctx, invitation.CreateInput{
			Email: "Invitee@Inv.Local", Scope: invitation.ScopeWorkspace,
			WorkspaceID: ws.ID, Role: workspace.RoleMember, InvitedBy: owner.ID,
		}, now)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if inv.Email != "invitee@inv.local" {
			t.Fatalf("email not lower-cased: %q", inv.Email)
		}
		// duplicate pending → ErrDuplicate
		if _, err := invStore.Create(ctx, invitation.CreateInput{
			Email: "invitee@inv.local", Scope: invitation.ScopeWorkspace,
			WorkspaceID: ws.ID, Role: workspace.RoleMember,
		}, now); !errors.Is(err, invitation.ErrDuplicate) {
			t.Fatalf("want ErrDuplicate, got %v", err)
		}

		// invitee logs in later (email arrives in a different case)
		invitee, _ := authStore.CreateUser(ctx, "usr_inv_ee", "invitee@inv.local", "Invitee", auth.RoleUser)
		claimed, err := invStore.ClaimForEmail(ctx, "INVITEE@inv.local", invitee.ID, now)
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		if len(claimed) != 1 {
			t.Fatalf("claimed = %d, want 1", len(claimed))
		}
		role, err := wsStore.GetRole(ctx, ws.ID, invitee.ID)
		if err != nil || role != workspace.RoleMember {
			t.Fatalf("membership role=%q err=%v", role, err)
		}
		// idempotent: a second claim finds nothing pending
		again, _ := invStore.ClaimForEmail(ctx, "invitee@inv.local", invitee.ID, now)
		if len(again) != 0 {
			t.Fatalf("second claim returned %d, want 0", len(again))
		}
	})

	t.Run("project invite claim → project_members row", func(t *testing.T) {
		if _, err := invStore.Create(ctx, invitation.CreateInput{
			Email: "pjinvitee@inv.local", Scope: invitation.ScopeProject,
			WorkspaceID: ws.ID, ProjectID: pj.ID, Role: project.RoleWrite, InvitedBy: owner.ID,
		}, now); err != nil {
			t.Fatalf("create project invite: %v", err)
		}
		u, _ := authStore.CreateUser(ctx, "usr_inv_pj", "pjinvitee@inv.local", "PjInvitee", auth.RoleUser)
		if _, err := invStore.ClaimForEmail(ctx, "pjinvitee@inv.local", u.ID, now); err != nil {
			t.Fatalf("claim project: %v", err)
		}
		role, err := pjStore.GetRole(ctx, pj.ID, u.ID)
		if err != nil || role != project.RoleWrite {
			t.Fatalf("project membership role=%q err=%v", role, err)
		}
	})

	t.Run("revoke before claim is not claimed", func(t *testing.T) {
		inv, _ := invStore.Create(ctx, invitation.CreateInput{
			Email: "revokee@inv.local", Scope: invitation.ScopeWorkspace,
			WorkspaceID: ws.ID, Role: workspace.RoleMember, InvitedBy: owner.ID,
		}, now)
		if err := invStore.Revoke(ctx, inv.ID, now); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		u, _ := authStore.CreateUser(ctx, "usr_inv_rev", "revokee@inv.local", "Revokee", auth.RoleUser)
		claimed, _ := invStore.ClaimForEmail(ctx, "revokee@inv.local", u.ID, now)
		if len(claimed) != 0 {
			t.Fatalf("revoked invite was claimed: %d", len(claimed))
		}
		if _, err := wsStore.GetRole(ctx, ws.ID, u.ID); !errors.Is(err, workspace.ErrMemberNotFound) {
			t.Fatalf("revoked invite created membership: %v", err)
		}
		// revoking again → ErrNotPending
		if err := invStore.Revoke(ctx, inv.ID, now); !errors.Is(err, invitation.ErrNotPending) {
			t.Fatalf("want ErrNotPending, got %v", err)
		}
	})
}

// TestInvitationVerbsAuthorization covers the canInvite gate and the
// already-registered immediate-claim (AC#4) through the verb surface.
func TestInvitationVerbsAuthorization(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	ctx := context.Background()
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	authStore := auth.New(env.DB)
	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	invStore := invitation.New(env.DB)
	verb.SetAuthStore(authStore)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	verb.SetInvitationStore(invStore)
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
		verb.SetInvitationStore(nil)
	})

	wsOwner, _ := authStore.CreateUser(ctx, "usr_iv_owner", "owner@iv.local", "Owner", auth.RoleUser)
	outsider, _ := authStore.CreateUser(ctx, "usr_iv_out", "out@iv.local", "Outsider", auth.RoleUser)
	ws, _ := wsStore.Create(ctx, wsOwner.ID, "iv-ws", now) // wsOwner is 'admin' member

	createReq := func(email string) json.RawMessage {
		b, _ := json.Marshal(map[string]string{
			"email": email, "scope": "workspace", "workspace_id": ws.ID, "role": "member",
		})
		return b
	}

	t.Run("workspace admin may invite", func(t *testing.T) {
		if _, err := verb.Dispatch(authWithUser(ctx, wsOwner), "invitation_create", createReq("a@iv.local")); err != nil {
			t.Fatalf("owner invite should succeed: %v", err)
		}
	})

	t.Run("outsider is forbidden", func(t *testing.T) {
		_, err := verb.Dispatch(authWithUser(ctx, outsider), "invitation_create", createReq("b@iv.local"))
		if err == nil {
			t.Fatal("outsider invite should be forbidden")
		}
	})

	t.Run("already-registered invitee claimed immediately", func(t *testing.T) {
		existing, _ := authStore.CreateUser(ctx, "usr_iv_exist", "exist@iv.local", "Exist", auth.RoleUser)
		raw, err := verb.Dispatch(authWithUser(ctx, wsOwner), "invitation_create", createReq("exist@iv.local"))
		if err != nil {
			t.Fatalf("invite existing: %v", err)
		}
		var inv invitation.Invitation
		_ = json.Unmarshal(raw, &inv)
		if inv.Status != invitation.StatusAccepted {
			t.Fatalf("status = %q, want accepted (immediate claim)", inv.Status)
		}
		role, err := wsStore.GetRole(ctx, ws.ID, existing.ID)
		if err != nil || role != workspace.RoleMember {
			t.Fatalf("existing user not added: role=%q err=%v", role, err)
		}
	})
}
