//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"strings"
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

// TestStoryDetailTabs_Chromedp drives a headless browser over the tabbed
// expanded story-detail content (sty_fdcf8297): the default Description/ACs tab,
// the Progress tab (live PROCESS trace), and the Ledger/Log tab (append-only
// ledger). Asserts each tab renders its content in the live DOM after a
// client-side (Alpine) switch — no reload.
func TestStoryDetailTabs_Chromedp(t *testing.T) {
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
	now := time.Date(2026, 6, 9, 3, 0, 0, 0, time.UTC)
	ws, err := wsStore.Create(ctx, "", "tabs-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "tabs-project"}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, _, err := docStore.Upsert(ctx, document.UpsertInput{
		Key:  document.Key{Scope: document.ScopeProject, WorkspaceID: ws.ID, ProjectID: pj.ID, Name: "tabs-fix-workflow"},
		Type: document.TypeSkill, Body: fixWorkflowBody("tabs-fix-workflow"),
	}, now); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}

	body := "## Overview\n\nTabbed story body with **markdown**.\n\n- alpha\n- beta\n"
	acs := "1. Tabs render.\n2. Ledger renders.\n"
	fixCat := "fix"
	inProgress := "in_progress"
	createReq, _ := json.Marshal(verb.DocumentUpsertRequest{
		Type: "story", ProjectID: pj.ID, Name: "tabbed story", Category: &fixCat,
		Body: body, AcceptanceCriteria: &acs,
	})
	createRaw, err := verb.Dispatch(ctx, "document_upsert", createReq)
	if err != nil {
		t.Fatalf("create story: %v", err)
	}
	var created verb.DocumentUpsertResponse
	if err := json.Unmarshal(createRaw, &created); err != nil {
		t.Fatalf("decode story: %v", err)
	}
	storyID := created.Document.ID
	patchReq, _ := json.Marshal(verb.DocumentUpsertRequest{ID: storyID, Status: &inProgress})
	if _, err := verb.Dispatch(ctx, "document_upsert", patchReq); err != nil {
		t.Fatalf("patch status: %v", err)
	}

	// Seed ledger history (reviewer-enacted kinds are role-gated through the
	// verb, so append via the store directly — as the QA test does).
	appendLedger := func(kind, b string, payload map[string]any, at time.Time) {
		t.Helper()
		pb, _ := json.Marshal(payload)
		if _, err := ledStore.Append(ctx, ledger.AppendInput{
			StoryID: storyID, ProjectID: pj.ID, WorkspaceID: ws.ID,
			Kind: kind, Body: b, Payload: pb, Actor: "usr_dev_admin",
		}, at); err != nil {
			t.Fatalf("append %s: %v", kind, err)
		}
	}
	appendLedger("review_accept", "plan ready to start", map[string]any{"gate": "satellites-story-plan-review", "from_status": "backlog", "to_status": "in_progress"}, now)
	appendLedger("status_transition", "backlog → in_progress", map[string]any{"from_status": "backlog", "to_status": "in_progress"}, now.Add(time.Second))

	bctx := newBrowserCtx(t)
	if err := chromedp.Run(bctx,
		chromedp.Navigate(env.ServerURL+"/login"),
		chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
		chromedp.Click(`button[data-action="dev-login-admin"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
		chromedp.Navigate(env.ServerURL+"/stories/"+storyID),
		chromedp.WaitVisible(`[data-field="story-tabs"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("nav: %v", err)
	}

	displayOf := func(t *testing.T, sel string) string {
		t.Helper()
		var d string
		if err := chromedp.Run(bctx, chromedp.Evaluate(
			`(() => { const el = document.querySelector('`+sel+`'); return el ? getComputedStyle(el).display : 'MISSING'; })()`, &d)); err != nil {
			t.Fatalf("display(%s): %v", sel, err)
		}
		return d
	}

	// AC1/AC2: default tab is Description/ACs — its panel is visible, the others
	// are hidden, and the ACs render.
	if d := displayOf(t, `[data-tabpanel="description"]`); d == "none" {
		t.Errorf("default Description tab should be visible, display=%q", d)
	}
	if d := displayOf(t, `[data-tabpanel="progress"]`); d != "none" {
		t.Errorf("Progress tab should be hidden by default, display=%q", d)
	}
	var acHTML string
	if err := chromedp.Run(bctx, chromedp.InnerHTML(`[data-field="story-acceptance"]`, &acHTML, chromedp.ByQuery)); err != nil {
		t.Fatalf("read acceptance: %v", err)
	}
	if !strings.Contains(acHTML, "Tabs render") {
		t.Errorf("AC1: acceptance criteria not rendered on default tab: %q", acHTML)
	}

	// AC2: switch to the Progress tab (client-side) — the PROCESS trace renders.
	if err := chromedp.Run(bctx,
		chromedp.Click(`[data-tab="progress"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-tabpanel="progress"] [data-table="process-trace"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("switch to Progress: %v", err)
	}
	if d := displayOf(t, `[data-tabpanel="description"]`); d != "none" {
		t.Errorf("Description tab should be hidden when Progress active, display=%q", d)
	}
	var traceRowCount int
	if err := chromedp.Run(bctx, chromedp.Evaluate(
		`document.querySelectorAll('[data-tabpanel="progress"] [data-row="transition"]').length`, &traceRowCount)); err != nil {
		t.Fatalf("count trace rows: %v", err)
	}
	if traceRowCount == 0 {
		t.Errorf("AC2: Progress tab shows no PROCESS-trace transition rows")
	}

	// AC2: switch to the Ledger/Log tab — the append-only ledger rows render.
	if err := chromedp.Run(bctx,
		chromedp.Click(`[data-tab="ledger"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-tabpanel="ledger"] [data-table="ledger-log"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("switch to Ledger: %v", err)
	}
	var ledgerRowCount int
	if err := chromedp.Run(bctx, chromedp.Evaluate(
		`document.querySelectorAll('[data-tabpanel="ledger"] [data-row="ledger-entry"]').length`, &ledgerRowCount)); err != nil {
		t.Fatalf("count ledger rows: %v", err)
	}
	if ledgerRowCount < 2 {
		t.Errorf("AC2: Ledger/Log tab shows %d rows, want >= 2 (seeded accept + transition)", ledgerRowCount)
	}
	var ledgerHTML string
	if err := chromedp.Run(bctx, chromedp.InnerHTML(`[data-table="ledger-log"]`, &ledgerHTML, chromedp.ByQuery)); err != nil {
		t.Fatalf("read ledger table: %v", err)
	}
	if !strings.Contains(ledgerHTML, "status_transition") || !strings.Contains(ledgerHTML, "plan ready to start") {
		t.Errorf("AC2: Ledger/Log tab missing expected entries: %q", ledgerHTML)
	}
}
