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
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
	"github.com/chromedp/chromedp"
)

// TestWorkspaceMounts_Chromedp is the DOGFOOD evidence for sty_1ede3853: a repo
// mounted readonly into a second workspace grants its members read (never
// write), the writable-single-home invariant holds, the home is untouched, the
// page shows the mounted repo read-only, and removing the mount drops the grant.
func TestWorkspaceMounts_Chromedp(t *testing.T) {
	env := testbootstrap.SetUpWithServer(t)

	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	docStore := document.New(env.DB)
	ledStore := ledger.New(env.DB)
	verb.SetAuthStore(env.Store)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	verb.SetDocumentStore(docStore)
	verb.SetLedgerStore(ledStore)
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
		verb.SetDocumentStore(nil)
		verb.SetLedgerStore(nil)
	})

	ctx := context.Background()
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	homeWS, err := wsStore.Create(ctx, "", "mount-home", now)
	if err != nil {
		t.Fatalf("create home workspace: %v", err)
	}
	mountWS, err := wsStore.Create(ctx, "", "mount-into", now)
	if err != nil {
		t.Fatalf("create mounting workspace: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{WorkspaceID: homeWS.ID, Name: "legacy-repo"}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	// The non-admin dev user is a MEMBER of the mounting workspace only — no
	// home membership, no project_members row, no api-key. Their sole possible
	// grant on the project is the mount.
	if err := wsStore.AddMember(ctx, mountWS.ID, "usr_dev_user", workspace.RoleMember, "usr_dev_admin", now); err != nil {
		t.Fatalf("add user to mounting workspace: %v", err)
	}

	admin, err := env.Store.GetUserByID(ctx, "usr_dev_admin")
	if err != nil || admin == nil {
		t.Fatalf("get admin: %v", err)
	}
	user, err := env.Store.GetUserByID(ctx, "usr_dev_user")
	if err != nil || user == nil {
		t.Fatalf("get user: %v", err)
	}
	ctxAdmin := auth.WithUser(ctx, admin)
	ctxUser := auth.WithUser(ctx, user)

	projectGet := func(c context.Context) error {
		req, _ := json.Marshal(verb.ProjectGetRequest{ID: pj.ID})
		_, err := verb.Dispatch(c, "project_get", req)
		return err
	}

	// Baseline: before any mount, the user has no access to the project.
	if err := projectGet(ctxUser); !errors.Is(err, verb.ErrForbidden) {
		t.Fatalf("baseline project_get by non-member: err = %v, want ErrForbidden", err)
	}

	// AC3: a writable mount is refused (mounts are readonly-only)…
	addReq, _ := json.Marshal(verb.WorkspaceMountAddRequest{WorkspaceID: mountWS.ID, ProjectID: pj.ID, Access: "write"})
	if _, err := verb.Dispatch(ctxAdmin, "workspace_mount_add", addReq); !errors.Is(err, verb.ErrBadRequest) {
		t.Errorf("mount_add writable: err = %v, want ErrBadRequest", err)
	}
	// …and mounting a repo into its own home is refused.
	selfReq, _ := json.Marshal(verb.WorkspaceMountAddRequest{WorkspaceID: homeWS.ID, ProjectID: pj.ID})
	if _, err := verb.Dispatch(ctxAdmin, "workspace_mount_add", selfReq); !errors.Is(err, verb.ErrBadRequest) {
		t.Errorf("mount_add into home: err = %v, want ErrBadRequest", err)
	}

	// AC1: a workspace admin mounts the repo readonly into the second workspace.
	okReq, _ := json.Marshal(verb.WorkspaceMountAddRequest{WorkspaceID: mountWS.ID, ProjectID: pj.ID})
	if _, err := verb.Dispatch(ctxAdmin, "workspace_mount_add", okReq); err != nil {
		t.Fatalf("mount_add readonly: %v", err)
	}

	// AC2/AC5a: the mounting workspace's member now resolves to read — can
	// project_get and document_list the project…
	if err := projectGet(ctxUser); err != nil {
		t.Errorf("project_get by mount member: %v, want success (read via mount)", err)
	}
	listReq, _ := json.Marshal(verb.DocumentListRequest{Scope: "project", ProjectID: pj.ID, WorkspaceID: homeWS.ID, Type: "document"})
	if _, err := verb.Dispatch(ctxUser, "document_list", listReq); err != nil {
		t.Errorf("document_list project by mount member: %v, want success (read)", err)
	}
	// AC2/AC5b: …but a document write through the mount is refused.
	writeReq, _ := json.Marshal(verb.DocumentUpsertRequest{
		Type: "document", Scope: "project", ProjectID: pj.ID, WorkspaceID: homeWS.ID,
		Name: "mount-write-attempt", Body: "should be refused",
	})
	if _, err := verb.Dispatch(ctxUser, "document_upsert", writeReq); !errors.Is(err, verb.ErrForbidden) {
		t.Errorf("document write through mount: err = %v, want ErrForbidden", err)
	}

	// AC5c: the home is untouched — project_list still lists it under its home,
	// and the mount does NOT appear as a home project of the mounting workspace.
	homeList := listProjectIDs(t, ctxAdmin, homeWS.ID)
	if !homeList[pj.ID] {
		t.Errorf("home project_list missing %s after mount", pj.ID)
	}
	if mountList := listProjectIDs(t, ctxAdmin, mountWS.ID); mountList[pj.ID] {
		t.Errorf("mounted project %s should not appear as a HOME project of the mounting workspace", pj.ID)
	}

	// AC1: the workspace page shows the mounted repo in the read-only section.
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), chromedpHeadlessOpts()...)
	t.Cleanup(cancelAlloc)
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	t.Cleanup(cancelBrowser)
	bctx, cancelRun := context.WithTimeout(browserCtx, 60*time.Second)
	t.Cleanup(cancelRun)

	var mountNames []string
	if err := chromedp.Run(bctx,
		chromedp.Navigate(env.ServerURL+"/login"),
		chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
		chromedp.Click(`button[data-action="dev-login-admin"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
		chromedp.Navigate(env.ServerURL+"/workspaces/"+mountWS.ID),
		chromedp.WaitVisible(`[data-section="mounted-repos"]`, chromedp.ByQuery),
		chromedp.Evaluate(`Array.from(document.querySelectorAll('[data-section="mounted-repos"] [data-field="workspace-mount-row"]')).map(e => e.dataset.mountName)`, &mountNames),
	); err != nil {
		t.Fatalf("workspace mounted-repos page: %v", err)
	}
	found := false
	for _, n := range mountNames {
		if n == "legacy-repo" {
			found = true
		}
	}
	if !found {
		t.Errorf("mounted repo %q missing from page mounted-repos section; got %v", "legacy-repo", mountNames)
	}

	// AC4: removing the mount drops the read grant.
	rmReq, _ := json.Marshal(verb.WorkspaceMountRemoveRequest{WorkspaceID: mountWS.ID, ProjectID: pj.ID})
	if _, err := verb.Dispatch(ctxAdmin, "workspace_mount_remove", rmReq); err != nil {
		t.Fatalf("mount_remove: %v", err)
	}
	if err := projectGet(ctxUser); !errors.Is(err, verb.ErrForbidden) {
		t.Errorf("project_get after mount removal: err = %v, want ErrForbidden", err)
	}
}

// listProjectIDs dispatches project_list for a workspace and returns the set of
// project ids it reports (home projects only).
func listProjectIDs(t *testing.T, ctx context.Context, wsID string) map[string]bool {
	t.Helper()
	req, _ := json.Marshal(verb.ProjectListRequest{WorkspaceID: wsID})
	raw, err := verb.Dispatch(ctx, "project_list", req)
	if err != nil {
		t.Fatalf("project_list %s: %v", wsID, err)
	}
	var resp verb.ProjectListResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode project_list: %v", err)
	}
	out := map[string]bool{}
	for _, p := range resp.Projects {
		out[p.ID] = true
	}
	return out
}
