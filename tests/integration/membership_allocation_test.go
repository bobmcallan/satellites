//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
	"github.com/chromedp/chromedp"
)

// TestMembershipAllocation is the DOGFOOD for sty_20687710 (epic:workspace-admin
// · membership-allocation): admin allocation over the existing membership
// surfaces — workspace cascade to read, project grant wins where higher,
// deallocation revokes access, and the orphaned-admin / non-admin guardrails.
func TestMembershipAllocation(t *testing.T) {
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
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)

	gAdmin, _ := env.Store.GetUserByID(ctx, "usr_dev_admin") // platform-admin
	alice, _ := env.Store.CreateUser(ctx, "usr_ma_alice", "alice@ma.local", "Alice", "user")
	bob, _ := env.Store.CreateUser(ctx, "usr_ma_bob", "bob@ma.local", "Bob", "user")

	ws, _ := wsStore.Create(ctx, gAdmin.ID, "alloc-ws", now) // gAdmin → admin member (sole admin)
	pj, _ := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "alloc-pj", OwnerUserID: gAdmin.ID}, now)

	disp := func(c context.Context, verbName, body string) (json.RawMessage, error) {
		return verb.Dispatch(c, verbName, json.RawMessage(body))
	}
	accessRole := func(t *testing.T, c context.Context) string {
		t.Helper()
		raw, err := disp(c, "project_access", `{"project_id":"`+pj.ID+`"}`)
		if err != nil {
			t.Fatalf("project_access: %v", err)
		}
		var r verb.ProjectAccessResponse
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Fatalf("decode project_access: %v", err)
		}
		return r.Role
	}
	forbidden := func(t *testing.T, err error) {
		t.Helper()
		if err == nil || !errors.Is(err, verb.ErrForbidden) {
			t.Fatalf("expected ErrForbidden, got %v", err)
		}
	}

	// AC3: a non-admin (bob) cannot allocate at either scope.
	t.Run("non-admin allocation refused", func(t *testing.T) {
		_, err := disp(authWithUser(ctx, bob), "workspace_member_add",
			`{"workspace_id":"`+ws.ID+`","user_id":"`+alice.ID+`","role":"member"}`)
		forbidden(t, err)
		_, err = disp(authWithUser(ctx, bob), "project_member_add",
			`{"project_id":"`+pj.ID+`","user_id":"`+alice.ID+`","role":"read"}`)
		forbidden(t, err)
	})

	// AC1/AC2/AC6: allocate alice to the workspace as a member → she resolves to
	// read on the home project (cascade) and can list it.
	if _, err := disp(authWithUser(ctx, gAdmin), "workspace_member_add",
		`{"workspace_id":"`+ws.ID+`","user_id":"`+alice.ID+`","role":"member"}`); err != nil {
		t.Fatalf("allocate workspace member: %v", err)
	}
	if got := accessRole(t, authWithUser(ctx, alice)); got != "read" {
		t.Fatalf("workspace cascade: alice's project role = %q, want read", got)
	}
	if raw, err := disp(authWithUser(ctx, alice), "project_list", `{"workspace_id":"`+ws.ID+`"}`); err != nil {
		t.Fatalf("alice list home projects: %v", err)
	} else {
		var pl verb.ProjectListResponse
		_ = json.Unmarshal(raw, &pl)
		found := false
		for _, p := range pl.Projects {
			if p.ID == pj.ID {
				found = true
			}
		}
		if !found {
			t.Fatalf("alice cannot list the home project after workspace allocation")
		}
	}

	// AC2/AC6: a project-level write grant wins over the workspace read cascade.
	if _, err := disp(authWithUser(ctx, gAdmin), "project_member_add",
		`{"project_id":"`+pj.ID+`","user_id":"`+alice.ID+`","role":"write"}`); err != nil {
		t.Fatalf("grant project write: %v", err)
	}
	if got := accessRole(t, authWithUser(ctx, alice)); got != "write" {
		t.Fatalf("project grant: alice's role = %q, want write (highest wins)", got)
	}

	// AC4 (idempotent): re-allocating the same project role updates rather than errors.
	if _, err := disp(authWithUser(ctx, gAdmin), "project_member_add",
		`{"project_id":"`+pj.ID+`","user_id":"`+alice.ID+`","role":"write"}`); err != nil {
		t.Fatalf("idempotent re-allocation errored: %v", err)
	}

	// AC4 (no orphaned admin): gAdmin is the sole owner/admin of the workspace —
	// removing them is refused.
	t.Run("remove last admin refused", func(t *testing.T) {
		_, err := disp(authWithUser(ctx, gAdmin), "workspace_member_remove",
			`{"workspace_id":"`+ws.ID+`","user_id":"`+gAdmin.ID+`"}`)
		forbidden(t, err)
	})

	// AC5 (deallocation revokes immediately): drop alice's project grant AND her
	// workspace membership; her subsequent document_get on the project is refused.
	seedDoc := func() {
		if _, err := disp(authWithUser(ctx, gAdmin), "document_upsert",
			`{"name":"readme","scope":"project","workspace_id":"`+ws.ID+`","project_id":"`+pj.ID+`","body":"x"}`); err != nil {
			t.Fatalf("seed doc: %v", err)
		}
	}
	seedDoc()
	// While allocated, alice can read it.
	if _, err := disp(authWithUser(ctx, alice), "document_get",
		`{"name":"readme","scope":"project","workspace_id":"`+ws.ID+`","project_id":"`+pj.ID+`"}`); err != nil {
		t.Fatalf("alice should read the doc while allocated: %v", err)
	}
	if _, err := disp(authWithUser(ctx, gAdmin), "project_member_remove",
		`{"project_id":"`+pj.ID+`","user_id":"`+alice.ID+`"}`); err != nil {
		t.Fatalf("deallocate project: %v", err)
	}
	if _, err := disp(authWithUser(ctx, gAdmin), "workspace_member_remove",
		`{"workspace_id":"`+ws.ID+`","user_id":"`+alice.ID+`"}`); err != nil {
		t.Fatalf("deallocate workspace: %v", err)
	}
	t.Run("deallocation revokes access immediately", func(t *testing.T) {
		_, err := disp(authWithUser(ctx, alice), "document_get",
			`{"name":"readme","scope":"project","workspace_id":"`+ws.ID+`","project_id":"`+pj.ID+`"}`)
		forbidden(t, err)
		if got := accessRole(t, authWithUser(ctx, alice)); got != "" {
			t.Fatalf("after deallocation alice's role = %q, want none", got)
		}
	})

	// AC6 (chromedp): the workspace page reflects membership. Re-allocate alice,
	// log in as the admin, and assert she appears in the members section.
	if _, err := disp(authWithUser(ctx, gAdmin), "workspace_member_add",
		`{"workspace_id":"`+ws.ID+`","user_id":"`+alice.ID+`","role":"member"}`); err != nil {
		t.Fatalf("re-allocate for portal check: %v", err)
	}

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), chromedpHeadlessOpts()...)
	t.Cleanup(cancelAlloc)
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	t.Cleanup(cancelBrowser)
	bctx, cancelRun := context.WithTimeout(browserCtx, 60*time.Second)
	t.Cleanup(cancelRun)

	var memberShown bool
	if err := chromedp.Run(bctx,
		chromedp.Navigate(env.ServerURL+"/login"),
		chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
		chromedp.Click(`button[data-action="dev-login-admin"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
		chromedp.Navigate(env.ServerURL+"/workspaces/"+ws.ID),
		chromedp.WaitVisible(`[data-section="workspace-members"]`, chromedp.ByQuery),
		chromedp.Evaluate(`!!document.querySelector('[data-field="workspace-member-row"][data-member-user="`+alice.ID+`"]')`, &memberShown),
	); err != nil {
		t.Fatalf("workspace members page: %v", err)
	}
	if !memberShown {
		t.Fatalf("workspace page does not reflect alice's membership")
	}
}
