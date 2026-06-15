//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
	"github.com/chromedp/chromedp"
)

// TestProjectsTypeChip_Chromedp drives the /projects page (sty_529dd0ea): each
// project's `type` renders as a tag-like chip, and selecting a type from the
// filter strip narrows the rendered project rows while clearing (ALL) restores
// the full list. The chip vocabulary is data-driven (no hardcoded enum).
func TestProjectsTypeChip_Chromedp(t *testing.T) {
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
	now := time.Now().UTC()
	ws, err := wsStore.Create(ctx, "", "type-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	// Three projects: two typed (skills, product) + one untyped (renders no chip).
	mk := func(name, typ string) {
		t.Helper()
		p, err := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: name}, now)
		if err != nil {
			t.Fatalf("create project %s: %v", name, err)
		}
		if typ != "" {
			if _, err := pjStore.Update(ctx, p.ID, project.UpdateInput{Type: &typ}, now); err != nil {
				t.Fatalf("set type %s: %v", name, err)
			}
		}
	}
	mk("publisher-repo", "skills")
	mk("product-repo", "product")
	mk("scratch-repo", "")

	bctx := newBrowserCtx(t)
	if err := chromedp.Run(bctx,
		chromedp.Navigate(env.ServerURL+"/login"),
		chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
		chromedp.Click(`button[data-action="dev-login-admin"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
		chromedp.Navigate(env.ServerURL+"/projects"),
		chromedp.WaitVisible(`[data-section="projects-list"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("login + navigate to /projects: %v", err)
	}

	count := func(sel string) int {
		var n int
		if err := chromedp.Run(bctx, chromedp.Evaluate(`document.querySelectorAll('`+sel+`').length`, &n)); err != nil {
			t.Fatalf("count %s: %v", sel, err)
		}
		return n
	}

	// AC1: the type chip renders. The two typed projects each carry a chip; the
	// untyped one does not (3 rows, 2 chips).
	if n := count(`[data-field="project-row"]`); n != 3 {
		t.Fatalf("expected 3 project rows, got %d", n)
	}
	if n := count(`[data-field="project-row"] [data-field="project-type"]`); n != 2 {
		t.Fatalf("expected 2 type chips (untyped project has none), got %d", n)
	}

	// AC2/AC3: the filter strip is data-driven — a chip per distinct type present.
	if n := count(`[data-field="project-type-filter"] [data-type-filter="skills"]`); n != 1 {
		t.Fatalf("expected a 'skills' filter chip, got %d", n)
	}
	if n := count(`[data-field="project-type-filter"] [data-type-filter="product"]`); n != 1 {
		t.Fatalf("expected a 'product' filter chip, got %d", n)
	}

	// AC2: selecting the 'skills' type narrows to the one skills project.
	if err := chromedp.Run(bctx,
		chromedp.Click(`[data-type-filter="skills"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-section="projects-list"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("click skills filter: %v", err)
	}
	if n := count(`[data-field="project-row"]`); n != 1 {
		t.Fatalf("after skills filter: expected 1 row, got %d", n)
	}
	var chipText string
	if err := chromedp.Run(bctx, chromedp.Evaluate(`(document.querySelector('[data-field="project-row"] [data-field="project-type"]')||{}).textContent || ''`, &chipText)); err != nil {
		t.Fatalf("chip text: %v", err)
	}
	if chipText != "skills" {
		t.Fatalf("after skills filter: chip text = %q, want skills", chipText)
	}

	// Clearing (ALL) restores the full list.
	if err := chromedp.Run(bctx,
		chromedp.Click(`[data-type-filter="all"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-section="projects-list"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("click all filter: %v", err)
	}
	if n := count(`[data-field="project-row"]`); n != 3 {
		t.Fatalf("after clearing filter: expected 3 rows, got %d", n)
	}
}
