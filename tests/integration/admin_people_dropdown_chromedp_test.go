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

// TestAdminPeopleWorkspaceDropdown pins epic:user-admin sty_14cf17e3: a global
// admin sees a workspace selector and can operate another workspace; a
// non-global user never sees the selector.
func TestAdminPeopleWorkspaceDropdown(t *testing.T) {
	env := testbootstrap.SetUpWithServer(t)
	ctx := context.Background()
	now := time.Now().UTC()

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

	// A separate workspace the admin does not personally own.
	teamWS, err := wsStore.Create(ctx, "usr_dev_user", "team-ws", now)
	if err != nil {
		t.Fatalf("create team workspace: %v", err)
	}

	t.Run("global admin sees dropdown, switches workspace, manages it", func(t *testing.T) {
		bctx := newBrowserCtx(t)
		if err := chromedp.Run(bctx,
			chromedp.Navigate(env.ServerURL+"/login"),
			chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
			chromedp.Click(`button[data-action="dev-login-admin"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("login admin: %v", err)
		}

		var hasSelector bool
		if err := chromedp.Run(bctx,
			chromedp.Navigate(env.ServerURL+"/settings/people"),
			chromedp.WaitVisible(`[data-field="admin-people"]`, chromedp.ByQuery),
			chromedp.Evaluate(`!!document.querySelector('[data-field="ws-selector"]')`, &hasSelector),
		); err != nil {
			t.Fatalf("load page: %v", err)
		}
		if !hasSelector {
			t.Fatal("global admin should see the workspace selector")
		}

		// Switch to team-ws via the selector (its onchange navigates), then
		// create a project — it must land in team-ws.
		var heading string
		var hasCreate bool
		if err := chromedp.Run(bctx,
			chromedp.Evaluate(`(()=>{const s=document.querySelector('[data-field="ws-selector"]');s.value='`+teamWS.ID+`';s.dispatchEvent(new Event('change'));return true;})()`, nil),
			chromedp.Sleep(500*time.Millisecond),
			chromedp.WaitVisible(`[data-field="admin-people"]`, chromedp.ByQuery),
			chromedp.Text(`[data-section="ws-members"] .panel-header h2`, &heading, chromedp.ByQuery),
			chromedp.Evaluate(`!!document.querySelector('[data-action="create-project-open"]')`, &hasCreate),
		); err != nil {
			t.Fatalf("switch workspace: %v", err)
		}
		if !strings.Contains(heading, "team-ws") {
			t.Fatalf("after switch, heading = %q, want it to name team-ws", heading)
		}
		if !hasCreate {
			t.Fatal("global admin should have admin controls on the selected workspace")
		}

		var landed bool
		if err := chromedp.Run(bctx,
			chromedp.Click(`[data-action="create-project-open"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-field="create-project-name"]`, chromedp.ByQuery),
			chromedp.SendKeys(`[data-field="create-project-name"]`, "teamproj", chromedp.ByQuery),
			chromedp.Click(`[data-action="create-project-submit"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-field="admin-people"]`, chromedp.ByQuery),
			chromedp.Sleep(400*time.Millisecond),
			chromedp.Evaluate(`Array.from(document.querySelectorAll('[data-field="project-row"]')).some(r=>r.textContent.includes('teamproj'))`, &landed),
		); err != nil {
			t.Fatalf("create project in team-ws: %v", err)
		}
		if !landed {
			t.Fatal("project created via dropdown should land in the selected workspace")
		}
		// Confirm server-side it really belongs to team-ws.
		projs, _ := pjStore.ListByWorkspace(ctx, teamWS.ID)
		found := false
		for _, p := range projs {
			if p.Name == "teamproj" {
				found = true
			}
		}
		if !found {
			t.Fatal("teamproj not found in team-ws on the server")
		}
	})

	t.Run("non-global user never sees the selector", func(t *testing.T) {
		bctx := newBrowserCtx(t)
		var selectorCount int
		if err := chromedp.Run(bctx,
			chromedp.Navigate(env.ServerURL+"/login"),
			chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
			chromedp.Click(`button[data-action="dev-login-user"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
			chromedp.Navigate(env.ServerURL+"/settings/people"),
			chromedp.WaitVisible(`[data-field="admin-people"]`, chromedp.ByQuery),
			chromedp.Evaluate(`document.querySelectorAll('[data-field="ws-selector"]').length`, &selectorCount),
		); err != nil {
			t.Fatalf("user view: %v", err)
		}
		if selectorCount != 0 {
			t.Fatal("non-global user must not see the workspace selector")
		}
	})
}
