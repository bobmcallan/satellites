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

// TestProjectDetailStoryTabs_Chromedp drives the project-detail inline story
// panel (sty_762730ad): expanding a story row shows readable markdown
// description/ACs, and tabs (Description/ACs · Progress · Ledger/Log) where
// Progress and Ledger lazy-load via fragment endpoints.
func TestProjectDetailStoryTabs_Chromedp(t *testing.T) {
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
	now := time.Date(2026, 6, 9, 4, 0, 0, 0, time.UTC)
	ws, err := wsStore.Create(ctx, "", "ptabs-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "ptabs-project"}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, _, err := docStore.Upsert(ctx, document.UpsertInput{
		Key:  document.Key{Scope: document.ScopeProject, WorkspaceID: ws.ID, ProjectID: pj.ID, Name: "ptabs-fix-workflow"},
		Type: document.TypeSkill, Body: fixWorkflowBody("ptabs-fix-workflow"),
	}, now); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}

	body := "## Overview\n\nInline panel body with **markdown**.\n\n- alpha item\n- beta item\n"
	acs := "1. Renders inline.\n2. Tabs work.\n"
	fixCat := "fix"
	inProgress := "in_progress"
	createReq, _ := json.Marshal(verb.DocumentUpsertRequest{
		Type: "story", ProjectID: pj.ID, Name: "inline tabbed story", Category: &fixCat,
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
		chromedp.Navigate(env.ServerURL+"/projects/"+pj.ID),
		chromedp.WaitVisible(`[data-field="stories-table"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`.story-row-title`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("nav: %v", err)
	}

	// Expand the story row, then the inline detail panel.
	if err := chromedp.Run(bctx,
		chromedp.Click(`.story-row-title`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-field="story-tabs"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("expand row: %v", err)
	}

	// AC1: description renders as markdown (not raw <pre>).
	var descHTML, acHTML string
	if err := chromedp.Run(bctx,
		chromedp.InnerHTML(`[data-field="story-description-body"]`, &descHTML, chromedp.ByQuery),
		chromedp.InnerHTML(`[data-field="story-acceptance-body"]`, &acHTML, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("read content: %v", err)
	}
	if !strings.Contains(descHTML, "<li>") || !strings.Contains(descHTML, "markdown") {
		t.Errorf("AC1: description not rendered as markdown: %q", descHTML)
	}
	if !strings.Contains(acHTML, "Renders inline") {
		t.Errorf("AC1: acceptance criteria missing: %q", acHTML)
	}

	// AC2/AC3: Progress tab lazy-loads the PROCESS trace.
	if err := chromedp.Run(bctx,
		chromedp.Click(`[data-tab="progress"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-field="story-progress"] [data-table="process-trace"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("progress tab: %v", err)
	}
	var traceRows int
	if err := chromedp.Run(bctx, chromedp.Evaluate(
		`document.querySelectorAll('[data-field="story-progress"] [data-row="transition"]').length`, &traceRows)); err != nil {
		t.Fatalf("count trace rows: %v", err)
	}
	if traceRows == 0 {
		t.Errorf("AC3: Progress tab loaded no PROCESS-trace rows")
	}

	// AC2/AC3: Ledger tab lazy-loads the append-only ledger.
	if err := chromedp.Run(bctx,
		chromedp.Click(`[data-tab="ledger"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-field="story-ledger"] [data-table="ledger-log"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("ledger tab: %v", err)
	}
	var ledgerRows int
	if err := chromedp.Run(bctx, chromedp.Evaluate(
		`document.querySelectorAll('[data-field="story-ledger"] [data-row="ledger-entry"]').length`, &ledgerRows)); err != nil {
		t.Fatalf("count ledger rows: %v", err)
	}
	if ledgerRows < 2 {
		t.Errorf("AC3: Ledger tab loaded %d rows, want >= 2", ledgerRows)
	}
}
