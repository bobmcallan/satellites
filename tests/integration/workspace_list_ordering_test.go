//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
	"github.com/chromedp/chromedp"
)

// TestWorkspaceListOrdering is the DOGFOOD for sty_fe3c7847
// (epic:workspace-admin-followup · workspace-list-ordering): the workspace list
// is ordered case-insensitive alpha-numerically by name, server-side, over the
// authz-filtered set — identical for the verb result and the portal page,
// independent of creation order. Asserted by RELATIVE order of three seeded
// names so any incidental personal/default workspace does not perturb it.
func TestWorkspaceListOrdering(t *testing.T) {
	env := testbootstrap.SetUpWithServer(t)

	wsStore := workspace.New(env.DB)
	verb.SetAuthStore(env.Store)
	verb.SetWorkspaceStore(wsStore)
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
	})

	ctx := context.Background()
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	devUser, err := env.Store.GetUserByID(ctx, "usr_dev_user")
	if err != nil {
		t.Fatalf("get dev user: %v", err)
	}

	// Created in reverse-of-sorted order. Case-insensitive sort must yield
	// alpha < Mike < zulu; a naive case-sensitive (ASCII) sort would wrongly
	// place "Mike" (M=77) before "alpha" (a=97), so the capital proves the fold.
	for _, name := range []string{"zulu", "Mike", "alpha"} {
		if _, err := wsStore.Create(ctx, devUser.ID, name, now); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	// relativeOrderOK reports whether alpha appears before Mike before zulu in the
	// given ordered list of names (ignoring any other entries).
	relativeOrderOK := func(names []string) bool {
		want := []string{"alpha", "Mike", "zulu"}
		seen := 0
		for _, n := range names {
			if seen < len(want) && n == want[seen] {
				seen++
			}
		}
		return seen == len(want)
	}

	// AC1/AC2: the verb result is alpha-numerically ordered for the caller.
	raw, err := verb.Dispatch(authWithUser(ctx, devUser), "workspace_list", nil)
	if err != nil {
		t.Fatalf("workspace_list: %v", err)
	}
	var resp verb.WorkspaceListResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	verbNames := make([]string, 0, len(resp.Workspaces))
	for _, w := range resp.Workspaces {
		verbNames = append(verbNames, w.Name)
	}
	if !relativeOrderOK(verbNames) {
		t.Fatalf("verb result not alpha-numerically ordered; got %v", verbNames)
	}

	// AC4 (chromedp): the portal /workspaces page renders the same order.
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), chromedpHeadlessOpts()...)
	t.Cleanup(cancelAlloc)
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	t.Cleanup(cancelBrowser)
	bctx, cancelRun := context.WithTimeout(browserCtx, 60*time.Second)
	t.Cleanup(cancelRun)

	var domNames []string
	if err := chromedp.Run(bctx,
		chromedp.Navigate(env.ServerURL+"/login"),
		chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
		chromedp.Click(`button[data-action="dev-login-user"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
		chromedp.Navigate(env.ServerURL+"/workspaces"),
		chromedp.WaitVisible(`[data-section="workspaces-list"]`, chromedp.ByQuery),
		chromedp.Evaluate(`Array.from(document.querySelectorAll('[data-field="workspace-row"] .project-card-title')).map(e => e.textContent.trim())`, &domNames),
	); err != nil {
		t.Fatalf("workspaces page render: %v", err)
	}
	if !relativeOrderOK(domNames) {
		t.Fatalf("portal /workspaces not alpha-numerically ordered; got %v", domNames)
	}
}
