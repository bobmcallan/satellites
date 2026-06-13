//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
	"github.com/chromedp/chromedp"
)

// TestWorkspaceMemberDisplayNames is the DOGFOOD for sty_fcf5477e
// (epic:workspace-admin-followup · workspace-member-display-names):
// workspace_member_list resolves a human-readable display name per member via
// the chain stored-name → email-local-part → raw-id, never the raw OAuth id as
// the primary label, with authz unchanged.
func TestWorkspaceMemberDisplayNames(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	ctx := context.Background()
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)

	authStore := auth.New(env.DB)
	wsStore := workspace.New(env.DB)
	verb.SetAuthStore(authStore)
	verb.SetWorkspaceStore(wsStore)
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
	})

	// owner has a stored profile name; named has one too; nameless has only an
	// email (→ local-part); stranger is not a member.
	owner, _ := authStore.CreateUser(ctx, "usr_oauth_google_owner", "owner@dn.local", "Olivia Owner", auth.RoleUser)
	named, _ := authStore.CreateUser(ctx, "usr_oauth_google_108501074081366940596", "bob@dn.local", "Bob McAllan", auth.RoleUser)
	nameless, _ := authStore.CreateUser(ctx, "usr_oauth_google_nameless", "alice@dn.local", "", auth.RoleUser)
	stranger, _ := authStore.CreateUser(ctx, "usr_oauth_google_stranger", "stranger@dn.local", "Stranger", auth.RoleUser)

	ws, _ := wsStore.Create(ctx, owner.ID, "members-demo", now)
	if err := wsStore.AddMember(ctx, ws.ID, named.ID, workspace.RoleMember, owner.ID, now); err != nil {
		t.Fatalf("add named member: %v", err)
	}
	if err := wsStore.AddMember(ctx, ws.ID, nameless.ID, workspace.RoleMember, owner.ID, now); err != nil {
		t.Fatalf("add nameless member: %v", err)
	}

	listNames := func(c context.Context) (map[string]string, error) {
		raw, err := verb.Dispatch(c, "workspace_member_list", mustJSON(t, verb.WorkspaceMemberListRequest{WorkspaceID: ws.ID}))
		if err != nil {
			return nil, err
		}
		var resp verb.WorkspaceMemberListResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		out := map[string]string{}
		for _, m := range resp.Members {
			out[m.UserID] = m.Name
		}
		return out, nil
	}

	// AC1/AC2/AC3: as a member, names resolve via the chain — stored name, then
	// email local-part, then raw id; never the raw id when a name/email exists.
	names, err := listNames(authWithUser(ctx, owner))
	if err != nil {
		t.Fatalf("member list: %v", err)
	}
	if got := names[named.ID]; got != "Bob McAllan" {
		t.Fatalf("named member display = %q, want %q (not the raw id)", got, "Bob McAllan")
	}
	if got := names[nameless.ID]; got != "alice" {
		t.Fatalf("nameless member should fall back to email local-part; got %q want %q", got, "alice")
	}
	if got := names[owner.ID]; got != "Olivia Owner" {
		t.Fatalf("owner display = %q, want %q", got, "Olivia Owner")
	}
	// The raw OAuth id is no longer the primary label for a named member.
	if names[named.ID] == named.ID {
		t.Fatalf("named member's primary label must not be the raw id %q", named.ID)
	}

	// AC4: a non-member list attempt is still refused ErrForbidden.
	if _, err := listNames(authWithUser(ctx, stranger)); !errors.Is(err, verb.ErrForbidden) {
		t.Fatalf("non-member list should be ErrForbidden; got %v", err)
	}
}

// TestWorkspaceMemberDisplayNames_Chromedp is the portal DOGFOOD: the workspace
// page members section renders the member's display name, not the raw OAuth id.
func TestWorkspaceMemberDisplayNames_Chromedp(t *testing.T) {
	env := testbootstrap.SetUpWithServer(t)

	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	docStore := document.New(env.DB)
	verb.SetAuthStore(env.Store)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	verb.SetDocumentStore(docStore)
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
		verb.SetDocumentStore(nil)
	})

	ctx := context.Background()
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)

	admin, err := env.Store.GetUserByID(ctx, "usr_dev_admin")
	if err != nil {
		t.Fatalf("get dev admin: %v", err)
	}
	const bobID = "usr_oauth_google_108501074081366940596"
	if _, err := env.Store.CreateUser(ctx, bobID, "bob@portal.local", "Bob McAllan", auth.RoleUser); err != nil {
		t.Fatalf("create bob: %v", err)
	}
	// Workspace owned by the dev admin (so the admin session sees the members
	// section), with Bob added as a member.
	ws, err := wsStore.Create(ctx, admin.ID, "member-name-demo", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := wsStore.AddMember(ctx, ws.ID, bobID, workspace.RoleMember, admin.ID, now); err != nil {
		t.Fatalf("add bob: %v", err)
	}

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), chromedpHeadlessOpts()...)
	t.Cleanup(cancelAlloc)
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	t.Cleanup(cancelBrowser)
	bctx, cancelRun := context.WithTimeout(browserCtx, 60*time.Second)
	t.Cleanup(cancelRun)

	// The member row for Bob renders "Bob McAllan" as the label, and the raw
	// OAuth id is kept only as the stable data attribute (not the visible text).
	var detail struct {
		Label    string `json:"label"`
		RawShown bool   `json:"rawShown"`
	}
	if err := chromedp.Run(bctx,
		chromedp.Navigate(env.ServerURL+"/login"),
		chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
		chromedp.Click(`button[data-action="dev-login-admin"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
		chromedp.Navigate(env.ServerURL+"/workspaces/"+ws.ID),
		chromedp.WaitVisible(`[data-field="workspace-members"]`, chromedp.ByQuery),
		chromedp.Evaluate(`(() => {
			const el = document.querySelector('[data-field="workspace-member-row"][data-member-user="`+bobID+`"]');
			const label = el ? el.textContent.trim() : '';
			const rows = Array.from(document.querySelectorAll('[data-field="workspace-member-row"]'));
			const rawShown = rows.some(r => r.textContent.trim() === '`+bobID+`');
			return { label, rawShown };
		})()`, &detail),
	); err != nil {
		t.Fatalf("workspace members render: %v", err)
	}
	if detail.Label != "Bob McAllan" {
		t.Fatalf("member label = %q, want %q", detail.Label, "Bob McAllan")
	}
	if detail.RawShown {
		t.Fatalf("raw OAuth id %q must not be a visible member label", bobID)
	}
}
