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

	t.Run("existing user invited to project by mixed-case email is registered", func(t *testing.T) {
		// Regression (sty_1a85bffd): the invite email and the user's stored
		// email differ only by case. The immediate claim must still create the
		// project_members row — previously users.email was stored verbatim and
		// the lower-cased invite lookup missed, so the invitee was only added to
		// the pending-invitations list and never registered as a member.
		pj, err := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "iv-pj"}, now)
		if err != nil {
			t.Fatalf("create project: %v", err)
		}
		member, _ := authStore.CreateUser(ctx, "usr_iv_mc", "Mixed.Case@IV.Local", "Mixed", auth.RoleUser)
		req, _ := json.Marshal(map[string]string{
			"email": "Mixed.Case@IV.Local", "scope": "project", "project_id": pj.ID, "role": project.RoleWrite,
		})
		raw, err := verb.Dispatch(authWithUser(ctx, wsOwner), "invitation_create", req)
		if err != nil {
			t.Fatalf("invite existing to project: %v", err)
		}
		var inv invitation.Invitation
		_ = json.Unmarshal(raw, &inv)
		if inv.Status != invitation.StatusAccepted {
			t.Fatalf("status = %q, want accepted (immediate claim)", inv.Status)
		}
		role, err := pjStore.GetRole(ctx, pj.ID, member.ID)
		if err != nil || role != project.RoleWrite {
			t.Fatalf("invited user not registered as project member: role=%q err=%v", role, err)
		}
	})
}

// TestInvitationLinks covers the link-invite store lifecycle (sty_8557f770):
// create-link (token + expiry, no membership yet), redeem → membership,
// double-redeem, expired, and unknown-token.
func TestInvitationLinks(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	ctx := context.Background()
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	authStore := auth.New(env.DB)
	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	invStore := invitation.New(env.DB)

	owner, _ := authStore.CreateUser(ctx, "usr_link_owner", "owner@link.local", "Owner", auth.RoleAdmin)
	ws, _ := wsStore.Create(ctx, owner.ID, "link-ws", now)
	pj, _ := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "link-pj"}, now)
	redeemer, _ := authStore.CreateUser(ctx, "usr_link_redeemer", "redeemer@link.local", "Redeemer", auth.RoleUser)

	newLink := func(role string) invitation.Invitation {
		inv, err := invStore.CreateLink(ctx, invitation.CreateLinkInput{
			Scope: invitation.ScopeProject, WorkspaceID: ws.ID, ProjectID: pj.ID,
			Role: role, InvitedBy: owner.ID,
		}, now)
		if err != nil {
			t.Fatalf("create link: %v", err)
		}
		return inv
	}

	t.Run("create link stores token + expiry, grants nothing yet", func(t *testing.T) {
		inv := newLink(project.RoleWrite)
		if inv.Token == "" || inv.ExpiresAt == nil {
			t.Fatalf("token/expiry missing: %+v", inv)
		}
		if inv.Status != invitation.StatusPending || inv.Email != "" {
			t.Fatalf("unexpected link invite: %+v", inv)
		}
		if _, err := pjStore.GetRole(ctx, pj.ID, redeemer.ID); !errors.Is(err, project.ErrMemberNotFound) {
			t.Fatalf("unredeemed link granted membership: %v", err)
		}
	})

	t.Run("redeem creates membership; double-redeem fails", func(t *testing.T) {
		inv := newLink(project.RoleWrite)
		redeemed, err := invStore.RedeemToken(ctx, inv.Token, redeemer.ID, now)
		if err != nil {
			t.Fatalf("redeem: %v", err)
		}
		if redeemed.Status != invitation.StatusAccepted || redeemed.AcceptedBy != redeemer.ID {
			t.Fatalf("redeemed = %+v", redeemed)
		}
		role, err := pjStore.GetRole(ctx, pj.ID, redeemer.ID)
		if err != nil || role != project.RoleWrite {
			t.Fatalf("role=%q err=%v", role, err)
		}
		if _, err := invStore.RedeemToken(ctx, inv.Token, redeemer.ID, now); !errors.Is(err, invitation.ErrNotPending) {
			t.Fatalf("double redeem want ErrNotPending, got %v", err)
		}
	})

	t.Run("expired link is rejected, grants nothing", func(t *testing.T) {
		inv := newLink(project.RoleRead)
		if _, err := env.DB.ExecContext(ctx,
			`UPDATE invitations SET expires_at = $1 WHERE token = $2`, now.Add(-time.Hour), inv.Token); err != nil {
			t.Fatalf("force-expire: %v", err)
		}
		fresh, _ := authStore.CreateUser(ctx, "usr_link_exp", "exp@link.local", "Exp", auth.RoleUser)
		if _, err := invStore.RedeemToken(ctx, inv.Token, fresh.ID, now); !errors.Is(err, invitation.ErrExpired) {
			t.Fatalf("want ErrExpired, got %v", err)
		}
		if _, err := pjStore.GetRole(ctx, pj.ID, fresh.ID); !errors.Is(err, project.ErrMemberNotFound) {
			t.Fatalf("expired link granted membership: %v", err)
		}
	})

	t.Run("unknown token is not found", func(t *testing.T) {
		if _, err := invStore.RedeemToken(ctx, "deadbeefdeadbeef", redeemer.ID, now); !errors.Is(err, invitation.ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
	})
}

// TestInvitationLinkVerbs covers link create + redeem through the verb surface
// (sty_8557f770): empty-email invitation_create returns a token + redeem_path,
// and invitation_redeem registers the authenticated caller as a member.
func TestInvitationLinkVerbs(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	ctx := context.Background()
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
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

	owner, _ := authStore.CreateUser(ctx, "usr_lv_owner", "owner@lv.local", "Owner", auth.RoleUser)
	ws, _ := wsStore.Create(ctx, owner.ID, "lv-ws", now)
	pj, _ := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "lv-pj"}, now)
	redeemer, _ := authStore.CreateUser(ctx, "usr_lv_redeemer", "redeemer@lv.local", "Redeemer", auth.RoleUser)

	// Link-mode create: empty email → token + redeem_path.
	createBody, _ := json.Marshal(map[string]string{"scope": "project", "project_id": pj.ID, "role": "write"})
	raw, err := verb.Dispatch(authWithUser(ctx, owner), "invitation_create", createBody)
	if err != nil {
		t.Fatalf("link create: %v", err)
	}
	var created struct {
		Token      string `json:"token"`
		RedeemPath string `json:"redeem_path"`
		Status     string `json:"status"`
	}
	_ = json.Unmarshal(raw, &created)
	if created.Token == "" || created.RedeemPath != "/invite/"+created.Token || created.Status != "pending" {
		t.Fatalf("unexpected link create response: %s", raw)
	}

	// Redeem as the (different) authenticated caller → membership.
	redeemBody, _ := json.Marshal(map[string]string{"token": created.Token})
	if _, err := verb.Dispatch(authWithUser(ctx, redeemer), "invitation_redeem", redeemBody); err != nil {
		t.Fatalf("redeem verb: %v", err)
	}
	role, err := pjStore.GetRole(ctx, pj.ID, redeemer.ID)
	if err != nil || role != project.RoleWrite {
		t.Fatalf("redeemer not registered: role=%q err=%v", role, err)
	}

	// Redeeming with no authenticated caller is forbidden.
	if _, err := verb.Dispatch(ctx, "invitation_redeem", redeemBody); err == nil {
		t.Fatal("unauthenticated redeem should be forbidden")
	}
}

// TestInviteDedupAndAutoMember covers sty_1c266e21: workspace owners/admins
// appear as implicit project members, and inviting an already-effective member
// is an already_member no-op (no new invitation row).
func TestInviteDedupAndAutoMember(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	ctx := context.Background()
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
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
	mj := func(v any) []byte { b, _ := json.Marshal(v); return b }

	owner, _ := authStore.CreateUser(ctx, "usr_dd_owner", "owner@dd.local", "Owner", auth.RoleUser)
	member, _ := authStore.CreateUser(ctx, "usr_dd_member", "member@dd.local", "Member", auth.RoleUser)
	ws, _ := wsStore.Create(ctx, owner.ID, "dd-ws", now) // owner is a workspace admin member
	pj, _ := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "dd-pj"}, now)

	listMembers := func() []project.Member {
		raw, err := verb.Dispatch(authWithUser(ctx, owner), "project_member_list", mj(map[string]string{"project_id": pj.ID}))
		if err != nil {
			t.Fatalf("member list: %v", err)
		}
		var resp verb.ProjectMemberListResponse
		_ = json.Unmarshal(raw, &resp)
		return resp.Members
	}
	find := func(ms []project.Member, uid string) *project.Member {
		for i := range ms {
			if ms[i].UserID == uid {
				return &ms[i]
			}
		}
		return nil
	}

	t.Run("workspace admin appears as implicit member", func(t *testing.T) {
		m := find(listMembers(), owner.ID)
		if m == nil || m.Source != project.SourceWorkspace || m.Role != project.RoleAdmin {
			t.Fatalf("workspace owner not an implicit admin member: %+v", listMembers())
		}
	})

	t.Run("explicit member is source=project; owner stays implicit", func(t *testing.T) {
		if err := pjStore.AddMember(ctx, pj.ID, member.ID, project.RoleWrite, owner.ID, now); err != nil {
			t.Fatalf("add explicit: %v", err)
		}
		ms := listMembers()
		if m := find(ms, member.ID); m == nil || m.Source != project.SourceProject {
			t.Fatalf("explicit member wrong source: %+v", ms)
		}
		if o := find(ms, owner.ID); o == nil || o.Source != project.SourceWorkspace {
			t.Fatalf("owner should remain implicit: %+v", ms)
		}
	})

	t.Run("re-invite explicit member → already_member, no new invite", func(t *testing.T) {
		raw, err := verb.Dispatch(authWithUser(ctx, owner), "invitation_create",
			mj(map[string]string{"email": "member@dd.local", "scope": "project", "project_id": pj.ID, "role": "read"}))
		if err != nil {
			t.Fatalf("invite member: %v", err)
		}
		var resp map[string]string
		_ = json.Unmarshal(raw, &resp)
		if resp["status"] != "already_member" {
			t.Fatalf("want already_member, got %s", raw)
		}
		invs, _ := invStore.List(ctx, invitation.ListInput{ProjectID: pj.ID, Status: "pending"})
		if len(invs) != 0 {
			t.Fatalf("dedup still created a pending invite: %+v", invs)
		}
	})

	t.Run("invite a workspace admin to the project → already_member", func(t *testing.T) {
		raw, err := verb.Dispatch(authWithUser(ctx, owner), "invitation_create",
			mj(map[string]string{"email": "owner@dd.local", "scope": "project", "project_id": pj.ID, "role": "read"}))
		if err != nil {
			t.Fatalf("invite owner: %v", err)
		}
		var resp map[string]string
		_ = json.Unmarshal(raw, &resp)
		if resp["status"] != "already_member" {
			t.Fatalf("want already_member for workspace admin, got %s", raw)
		}
	})
}
