//go:build integration

package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
	"github.com/chromedp/chromedp"
)

// TestLibraryPage_Chromedp drives the /library page (sty_b2f77307, tasks-only
// sty_98956dbb): the navbar item is present, published library-scope TASKS
// render (name/description/publisher), and the surface is tasks-only — a
// library-scope SKILL never appears and there are no kind-filter chips.
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

	// A published library-scope TASK — the only thing the library surfaces. Its
	// description resolves from the body frontmatter.
	taskBody := "---\ndescription: Generate the repo's high-level codegraph.\n---\n# Codegraph\n\n## Task\n\nGenerate the codegraph.\n"
	if _, _, err := docStore.Upsert(ctx, document.UpsertInput{
		Key:  document.Key{Scope: document.ScopeLibrary, ProjectID: pub.ID, Name: "Codegraph"},
		Type: document.TypeTask, Body: taskBody, CreatedBy: "system:test",
	}, now); err != nil {
		t.Fatalf("seed library task: %v", err)
	}
	// A library-scope SKILL — tasks-only means this must NOT list on /library.
	skillBody := "---\nname: corpus-summarise\nkind: task\ndescription: Summarise the workspace corpus.\n---\n## Spec\nSummarise.\n"
	if _, _, err := docStore.Upsert(ctx, document.UpsertInput{
		Key:  document.Key{Scope: document.ScopeLibrary, ProjectID: pub.ID, Name: "corpus-summarise"},
		Type: document.TypeSkill, Body: skillBody, CreatedBy: "system:test",
	}, now); err != nil {
		t.Fatalf("seed library skill: %v", err)
	}

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

	// AC1: exactly the one published task renders, with its description.
	if n := count(`[data-field="task-row"]`); n != 1 {
		t.Fatalf("expected 1 library task row, got %d", n)
	}
	var taskDesc string
	if err := chromedp.Run(bctx, chromedp.Evaluate(`(document.querySelector('[data-task-name="Codegraph"] .task-desc')||{}).textContent || ''`, &taskDesc)); err != nil {
		t.Fatalf("task desc: %v", err)
	}
	if !strings.Contains(taskDesc, "Generate the repo's high-level codegraph") {
		t.Fatalf("task description did not render, got %q", taskDesc)
	}

	// AC1/AC3: tasks-only — the library skill never lists, and there is no skill markup.
	var bodyHTML string
	if err := chromedp.Run(bctx, chromedp.Evaluate(`document.body.innerHTML`, &bodyHTML)); err != nil {
		t.Fatalf("read body html: %v", err)
	}
	if strings.Contains(bodyHTML, "corpus-summarise") {
		t.Errorf("a library skill leaked onto the tasks-only library page")
	}
	if count(`[data-skill-name]`) != 0 || count(`[data-skill-kind]`) != 0 {
		t.Errorf("skill-row markup present on the tasks-only library page")
	}

	// AC2: no kind-filter chips anywhere on the page.
	if count(`[data-kind-filter]`) != 0 {
		t.Errorf("kind-filter chips still rendered on the tasks-only library page")
	}
}
