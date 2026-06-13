//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
	"github.com/chromedp/chromedp"
)

// TestWorkspaceSurface_Chromedp is the DOGFOOD evidence for sty_67a66574: with
// a workspace seeded with two home repos, the portal lists the workspace under
// a "Workspaces" navbar entry, and the workspace page renders both repos with
// the caller's access role plus the objective placeholder.
func TestWorkspaceSurface_Chromedp(t *testing.T) {
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
	ws, err := wsStore.Create(ctx, "", "ws-surface-demo", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	for _, name := range []string{"alpha-repo", "beta-repo"} {
		if _, err := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: name}, now); err != nil {
			t.Fatalf("create project %s: %v", name, err)
		}
	}

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), chromedpHeadlessOpts()...)
	t.Cleanup(cancelAlloc)
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	t.Cleanup(cancelBrowser)
	bctx, cancelRun := context.WithTimeout(browserCtx, 60*time.Second)
	t.Cleanup(cancelRun)

	// AC1: log in as the dev admin, the navbar carries a Workspaces entry, and
	// the list includes the seeded workspace.
	var navPresent, wsLinkPresent bool
	if err := chromedp.Run(bctx,
		chromedp.Navigate(env.ServerURL+"/login"),
		chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
		chromedp.Click(`button[data-action="dev-login-admin"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
		chromedp.Navigate(env.ServerURL+"/workspaces"),
		chromedp.WaitVisible(`[data-section="workspaces-list"]`, chromedp.ByQuery),
		chromedp.Evaluate(`!!document.querySelector('.primary-nav a[data-nav="workspaces"]')`, &navPresent),
		chromedp.Evaluate(`!!document.querySelector('a[href="/workspaces/`+ws.ID+`"]')`, &wsLinkPresent),
	); err != nil {
		t.Fatalf("workspaces list: %v", err)
	}
	if !navPresent {
		t.Error("navbar has no Workspaces entry")
	}
	if !wsLinkPresent {
		t.Fatalf("workspace %s not listed on /workspaces", ws.ID)
	}

	// AC2/AC3: the workspace page shows the name, both repos with access role,
	// and the objective placeholder.
	var detail struct {
		Name         string   `json:"name"`
		Repos        []string `json:"repos"`
		Roles        []string `json:"roles"`
		HasObjective bool     `json:"hasObjective"`
	}
	if err := chromedp.Run(bctx,
		chromedp.Navigate(env.ServerURL+"/workspaces/"+ws.ID),
		chromedp.WaitVisible(`[data-section="workspace-detail"]`, chromedp.ByQuery),
		chromedp.Evaluate(`(() => {
			const name = (document.querySelector('[data-field="workspace-name"]') || {}).textContent || '';
			const rows = Array.from(document.querySelectorAll('[data-field="workspace-project-row"]'));
			const repos = rows.map(r => ((r.querySelector('.project-card-title') || {}).textContent || '').trim());
			const roles = rows.map(r => ((r.querySelector('[data-field="project-role"]') || {}).textContent || '').trim());
			return {
				name: name.trim(),
				repos: repos,
				roles: roles,
				hasObjective: !!document.querySelector('[data-section="objective"] [data-field="objective-placeholder"]'),
			};
		})()`, &detail),
	); err != nil {
		t.Fatalf("workspace detail: %v", err)
	}
	if detail.Name != "ws-surface-demo" {
		t.Errorf("workspace name = %q, want %q", detail.Name, "ws-surface-demo")
	}
	repoSet := map[string]bool{}
	for _, r := range detail.Repos {
		repoSet[r] = true
	}
	for _, want := range []string{"alpha-repo", "beta-repo"} {
		if !repoSet[want] {
			t.Errorf("repo %q missing from workspace page; got %v", want, detail.Repos)
		}
	}
	if len(detail.Roles) != 2 {
		t.Fatalf("expected 2 repo role cells, got %d (%v)", len(detail.Roles), detail.Roles)
	}
	for _, role := range detail.Roles {
		// The dev admin is a global admin → admin on every project.
		if role != "admin" {
			t.Errorf("repo access role = %q, want admin", role)
		}
	}
	if !detail.HasObjective {
		t.Error("objective placeholder missing on workspace page")
	}
}
