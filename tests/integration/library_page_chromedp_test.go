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

// TestLibraryPage_Chromedp drives the /library skill-library page (sty_b2f77307):
// the navbar item is present, library-scope skills render (name/kind/description/
// publisher), and selecting a kind filter narrows the rendered rows while
// clearing restores the full list.
func TestLibraryPage_Chromedp(t *testing.T) {
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
	ws, err := wsStore.Create(ctx, "", "lib-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pub, err := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "lib-publisher"}, now)
	if err != nil {
		t.Fatalf("create publisher project: %v", err)
	}

	// Two library skills of different kinds so the kind filter has something to
	// narrow. The kind + description resolve from the skill's frontmatter.
	seedSkill := func(name, kind, desc string) {
		t.Helper()
		body := "---\nname: " + name + "\nkind: " + kind + "\ndescription: " + desc + "\n---\n## Spec\n" + desc + "\n"
		if _, _, err := docStore.Upsert(ctx, document.UpsertInput{
			Key:  document.Key{Scope: document.ScopeLibrary, ProjectID: pub.ID, Name: name},
			Type: document.TypeSkill, Body: body, CreatedBy: "system:test",
		}, now); err != nil {
			t.Fatalf("seed skill %s: %v", name, err)
		}
	}
	seedSkill("corpus-summarise", "task", "Summarise the workspace corpus.")
	seedSkill("ship-it", "workflow", "The shipping lifecycle.")

	bctx := newBrowserCtx(t)
	if err := chromedp.Run(bctx,
		chromedp.Navigate(env.ServerURL+"/login"),
		chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
		chromedp.Click(`button[data-action="dev-login-admin"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
		chromedp.Navigate(env.ServerURL+"/library"),
		chromedp.WaitVisible(`[data-field="library-table"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("login + navigate to /library: %v", err)
	}

	count := func(sel string) int {
		var n int
		if err := chromedp.Run(bctx, chromedp.Evaluate(`document.querySelectorAll('`+sel+`').length`, &n)); err != nil {
			t.Fatalf("count %s: %v", sel, err)
		}
		return n
	}

	// AC1: the navbar item links to /library.
	var navHref string
	if err := chromedp.Run(bctx, chromedp.Evaluate(`(document.querySelector('[data-nav="library"]')||{}).getAttribute && document.querySelector('[data-nav="library"]').getAttribute('href')`, &navHref)); err != nil {
		t.Fatalf("nav href: %v", err)
	}
	if navHref != "/library" {
		t.Fatalf("navbar library item href = %q, want /library", navHref)
	}

	// AC2: both library skills render.
	if n := count(`[data-field="skill-row"]`); n != 2 {
		t.Fatalf("expected 2 library skill rows, got %d", n)
	}
	// Description + publisher rendered for the task skill.
	var taskDesc string
	if err := chromedp.Run(bctx, chromedp.Evaluate(`(document.querySelector('[data-skill-name="corpus-summarise"] .skill-desc')||{}).textContent || ''`, &taskDesc)); err != nil {
		t.Fatalf("task desc: %v", err)
	}
	if taskDesc == "" {
		t.Fatal("task skill description did not render")
	}

	// AC3: selecting the "task" kind filter narrows to one row.
	if err := chromedp.Run(bctx,
		chromedp.Click(`[data-kind-filter="task"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-field="library-table"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("click task filter: %v", err)
	}
	if n := count(`[data-field="skill-row"]`); n != 1 {
		t.Fatalf("after task filter: expected 1 row, got %d", n)
	}
	if n := count(`[data-skill-kind="workflow"]`); n != 0 {
		t.Fatalf("after task filter: workflow row should be hidden, got %d", n)
	}

	// Clearing (ALL) restores the full list.
	if err := chromedp.Run(bctx,
		chromedp.Click(`[data-kind-filter="all"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-field="library-table"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("click all filter: %v", err)
	}
	if n := count(`[data-field="skill-row"]`); n != 2 {
		t.Fatalf("after clearing filter: expected 2 rows, got %d", n)
	}
}
