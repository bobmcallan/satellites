//go:build integration

package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/invitation"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
	"github.com/chromedp/chromedp"
)

// TestAdminPeoplePortal drives the /settings/people page (epic:user-admin
// sty_2293eb68): an admin creates a project, invites by email, changes a
// project member's role, and cancels an invite; a non-admin (read-only)
// viewer sees no management controls.
func TestAdminPeoplePortal(t *testing.T) {
	env := testbootstrap.SetUpWithServer(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// The httptest server (server.Build) wires only the auth store; the verb
	// substrate stores are wired by main.go at boot. Replicate that wiring so
	// the portal handler's verb.Dispatch calls resolve.
	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	verb.SetAuthStore(env.Store)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	verb.SetInvitationStore(invitation.New(env.DB))
	verb.SetDocumentStore(document.New(env.DB))
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
		verb.SetInvitationStore(nil)
		verb.SetDocumentStore(nil)
	})
	adminWS, err := wsStore.GetPersonalForUser(ctx, "usr_dev_admin")
	if err != nil {
		t.Fatalf("admin personal workspace: %v", err)
	}
	setupPj, err := pjStore.Create(ctx, project.CreateInput{WorkspaceID: adminWS.ID, Name: "setup-proj"}, now)
	if err != nil {
		t.Fatalf("create setup project: %v", err)
	}
	if err := pjStore.AddMember(ctx, setupPj.ID, "usr_dev_user", project.RoleRead, "usr_dev_admin", now); err != nil {
		t.Fatalf("add project member: %v", err)
	}

	loginAdmin := func(bctx context.Context) error {
		return chromedp.Run(bctx,
			chromedp.Navigate(env.ServerURL+"/login"),
			chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
			chromedp.Click(`button[data-action="dev-login-admin"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
		)
	}

	t.Run("admin flows: create project, invite, cancel", func(t *testing.T) {
		bctx := newBrowserCtx(t)
		if err := loginAdmin(bctx); err != nil {
			t.Fatalf("login: %v", err)
		}

		var ownerSeen bool
		if err := chromedp.Run(bctx,
			chromedp.Navigate(env.ServerURL+"/settings/people"),
			chromedp.WaitVisible(`[data-field="admin-people"]`, chromedp.ByQuery),
			chromedp.Evaluate(`!!Array.from(document.querySelectorAll('[data-field="member-row"] [data-field="member-role"]')).find(e=>e.textContent.trim()==='owner')`, &ownerSeen),
		); err != nil {
			t.Fatalf("load page: %v", err)
		}
		if !ownerSeen {
			t.Fatal("admin should appear as workspace owner")
		}

		// Create a project via the form.
		var hasUIProj bool
		if err := chromedp.Run(bctx,
			chromedp.Click(`[data-action="create-project-open"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-field="create-project-name"]`, chromedp.ByQuery),
			chromedp.SendKeys(`[data-field="create-project-name"]`, "uiproj", chromedp.ByQuery),
			chromedp.Click(`[data-action="create-project-submit"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-field="admin-people"]`, chromedp.ByQuery),
			chromedp.Sleep(400*time.Millisecond),
			chromedp.Evaluate(`Array.from(document.querySelectorAll('[data-field="project-row"]')).some(r=>r.textContent.includes('uiproj'))`, &hasUIProj),
		); err != nil {
			t.Fatalf("create project: %v", err)
		}
		if !hasUIProj {
			t.Fatal("created project 'uiproj' should be listed")
		}

		// Invite by email to the workspace.
		var hasInvite bool
		if err := chromedp.Run(bctx,
			chromedp.Click(`[data-action="ws-invite-open"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-field="ws-invite-email"]`, chromedp.ByQuery),
			chromedp.SendKeys(`[data-field="ws-invite-email"]`, "invitee@ui.test", chromedp.ByQuery),
			chromedp.Click(`[data-action="ws-invite-submit"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-field="ws-invites-table"]`, chromedp.ByQuery),
			chromedp.Sleep(300*time.Millisecond),
			chromedp.Evaluate(`Array.from(document.querySelectorAll('[data-field="invite-email"]')).some(e=>e.textContent.includes('invitee@ui.test'))`, &hasInvite),
		); err != nil {
			t.Fatalf("invite: %v", err)
		}
		if !hasInvite {
			t.Fatal("invitation for invitee@ui.test should be listed")
		}

		// Cancel the invite.
		var inviteGone bool
		if err := chromedp.Run(bctx,
			chromedp.Click(`[data-action="invite-revoke"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-field="admin-people"]`, chromedp.ByQuery),
			chromedp.Sleep(400*time.Millisecond),
			chromedp.Evaluate(`!Array.from(document.querySelectorAll('[data-field="invite-email"]')).some(e=>e.textContent.includes('invitee@ui.test'))`, &inviteGone),
		); err != nil {
			t.Fatalf("cancel invite: %v", err)
		}
		if !inviteGone {
			t.Fatal("invitation should be gone after cancel")
		}
	})

	t.Run("admin changes a project member's role", func(t *testing.T) {
		bctx := newBrowserCtx(t)
		if err := loginAdmin(bctx); err != nil {
			t.Fatalf("login: %v", err)
		}
		var roleBefore, roleAfter string
		if err := chromedp.Run(bctx,
			chromedp.Navigate(env.ServerURL+"/settings/people?project_id="+setupPj.ID),
			chromedp.WaitVisible(`[data-field="pj-members-table"]`, chromedp.ByQuery),
			chromedp.Text(`[data-field="pj-member-row"] [data-field="pj-member-role"]`, &roleBefore, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("load project pane: %v", err)
		}
		if strings.TrimSpace(roleBefore) != "read" {
			t.Fatalf("member role before = %q, want read", roleBefore)
		}
		// Change the role to write by setting the select + submitting its form.
		if err := chromedp.Run(bctx,
			chromedp.Evaluate(`(()=>{const f=document.querySelector('[data-form="pj-member-role"]');f.querySelector('select').value='write';f.submit();return true;})()`, nil),
			chromedp.Sleep(500*time.Millisecond),
			chromedp.WaitVisible(`[data-field="pj-members-table"]`, chromedp.ByQuery),
			chromedp.Text(`[data-field="pj-member-row"] [data-field="pj-member-role"]`, &roleAfter, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("change role: %v", err)
		}
		if strings.TrimSpace(roleAfter) != "write" {
			t.Fatalf("member role after = %q, want write", roleAfter)
		}
	})

	t.Run("non-admin read-only viewer sees no management controls", func(t *testing.T) {
		bctx := newBrowserCtx(t)
		if err := chromedp.Run(bctx,
			chromedp.Navigate(env.ServerURL+"/login"),
			chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
			chromedp.Click(`button[data-action="dev-login-user"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("login user: %v", err)
		}
		var tableVisible bool
		var inviteControls int
		if err := chromedp.Run(bctx,
			chromedp.Navigate(env.ServerURL+"/settings/people?project_id="+setupPj.ID),
			chromedp.WaitVisible(`[data-field="pj-members-table"]`, chromedp.ByQuery),
			chromedp.Evaluate(`!!document.querySelector('[data-field="pj-members-table"]')`, &tableVisible),
			chromedp.Evaluate(`document.querySelectorAll('[data-action="pj-invite-open"]').length`, &inviteControls),
		); err != nil {
			t.Fatalf("user view project: %v", err)
		}
		if !tableVisible {
			t.Fatal("read-only member should still see the member table")
		}
		if inviteControls != 0 {
			t.Fatal("read-only member must not see project invite controls")
		}
	})
}
