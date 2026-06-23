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

// TestProjectDocumentsPanel_Chromedp drives the project-page Documents panel
// (epic:phases-task-outputs): the panel lists the project's type:document rows
// (AC1), the docs_q chip filter narrows them server-side (AC2), a text document
// expands inline to its rendered body (AC3/AC6), and the upload control is
// present (AC4). Read path goes through the same document verbs as the agent/CLI.
func TestProjectDocumentsPanel_Chromedp(t *testing.T) {
	// Quarantined like the sibling project-page chromedp tests
	// (TestProjectDetailPanel_Chromedp): headless Chrome is unreachable in CI and
	// the local integration tier. Verification on a Chrome-capable runner is
	// tracked by sty_6e44db4b (verify-project-documents-chromedp-test). The panel's
	// non-browser logic is covered by the unit tests in internal/server.
	skipInCI(t, "project-documents chromedp needs headless Chrome; verify on a capable runner — follow-up sty_6e44db4b")
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
	ws, err := wsStore.Create(ctx, "", "docs-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "docs-pj"}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	seedDoc := func(name, phase, body string) {
		t.Helper()
		doc, _, err := docStore.Upsert(ctx, document.UpsertInput{
			Key:       document.Key{Scope: document.ScopeProject, WorkspaceID: ws.ID, ProjectID: pj.ID, Name: name},
			Type:      document.TypeDocument,
			Body:      body,
			CreatedBy: "system:test",
		}, now)
		if err != nil {
			t.Fatalf("seed doc %s: %v", name, err)
		}
		if _, err := docStore.SetDocumentTags(ctx, doc.ID, []string{"type:document", "phase:" + phase}, now); err != nil {
			t.Fatalf("tag doc %s: %v", name, err)
		}
	}
	seedDoc("discovery-note", "discovery", "# Discovery note\n\nThe discovery body marker.")
	seedDoc("build-note", "build", "# Build note\n\nThe build body marker.")

	// 60s deadline — the project page renders three panels (stories/tasks/docs),
	// more than the landing-sized newBrowserCtx budget (mirrors
	// project_detail_chromedp_test.go).
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), chromedpHeadlessOpts()...)
	t.Cleanup(cancelAlloc)
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	t.Cleanup(cancelBrowser)
	bctx, cancelRun := context.WithTimeout(browserCtx, 60*time.Second)
	t.Cleanup(cancelRun)

	if err := chromedp.Run(bctx,
		chromedp.Navigate(env.ServerURL+"/login"),
		chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
		chromedp.Click(`button[data-action="dev-login-admin"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
		chromedp.Navigate(env.ServerURL+"/projects/"+pj.ID),
		chromedp.WaitVisible(`[data-section="project-documents"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-field="documents-table"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("login + navigate to project page: %v", err)
	}

	count := func(sel string) int {
		var n int
		if err := chromedp.Run(bctx, chromedp.Evaluate(`document.querySelectorAll('`+sel+`').length`, &n)); err != nil {
			t.Fatalf("count %s: %v", sel, err)
		}
		return n
	}

	// AC1: both project documents list in the panel.
	if got := count(`[data-field="document-row"]`); got != 2 {
		t.Fatalf("document rows = %d, want 2 (both project docs listed)", got)
	}

	// AC4: the upload control is present.
	if got := count(`[data-field="project-doc-upload"]`); got != 1 {
		t.Fatalf("upload dropzone count = %d, want 1", got)
	}

	// AC2: the docs_q chip filter narrows server-side to the discovery doc.
	if err := chromedp.Run(bctx,
		chromedp.Navigate(env.ServerURL+"/projects/"+pj.ID+"?docs_q=phase:discovery"),
		chromedp.WaitVisible(`[data-field="documents-table"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("navigate with docs_q filter: %v", err)
	}
	if got := count(`[data-field="document-row"]`); got != 1 {
		t.Fatalf("filtered document rows = %d, want 1 (phase:discovery only)", got)
	}

	// AC3/AC6: clicking the row (the title, away from the @click.stop chips)
	// expands its rendered body inline.
	if err := chromedp.Run(bctx,
		chromedp.Click(`[data-field="document-row-title"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-field="document-content-body"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("expand document row: %v", err)
	}
	var bodyText string
	if err := chromedp.Run(bctx, chromedp.Text(`[data-field="document-content-body"]`, &bodyText, chromedp.ByQuery)); err != nil {
		t.Fatalf("read expanded body: %v", err)
	}
	if !strings.Contains(bodyText, "discovery body marker") {
		t.Fatalf("expanded body = %q, want it to contain the discovery body marker", bodyText)
	}
}
