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

// TestStoryDetailExpandable_Chromedp drives a headless browser over the
// redesigned story-detail page (sty_c04acfc7): responsive width, the expanded
// header (status pill + live indicator), the readable markdown description/ACs,
// and the Alpine expand/collapse toggle. Asserts the *rendered* DOM, not just
// the server HTML, so an Alpine-bind regression is caught.
func TestStoryDetailExpandable_Chromedp(t *testing.T) {
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
	now := time.Date(2026, 6, 9, 2, 0, 0, 0, time.UTC)
	ws, err := wsStore.Create(ctx, "", "expand-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "expand-project"}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	// Project-scoped workflow so the PROCESS trace renders (no-regression check).
	if _, _, err := docStore.Upsert(ctx, document.UpsertInput{
		Key:  document.Key{Scope: document.ScopeProject, WorkspaceID: ws.ID, ProjectID: pj.ID, Name: "detail-fix-workflow"},
		Type: document.TypeSkill, Body: fixWorkflowBody("detail-fix-workflow"),
	}, now); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}

	// A fix story with a markdown body + ACs, in_progress.
	body := "## Overview\n\nThis story does **important** work.\n\n- first item\n- second item\n"
	acs := "1. First criterion holds.\n2. Second criterion holds.\n"
	fixCat := "fix"
	inProgress := "in_progress"
	createReq, _ := json.Marshal(verb.DocumentUpsertRequest{
		Type: "story", ProjectID: pj.ID, Name: "expandable story", Category: &fixCat,
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

	bctx := newBrowserCtx(t)

	// Login + navigate to the story-detail page.
	if err := chromedp.Run(bctx,
		chromedp.Navigate(env.ServerURL+"/login"),
		chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
		chromedp.Click(`button[data-action="dev-login-admin"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
		chromedp.Navigate(env.ServerURL+"/stories/"+storyID),
		chromedp.WaitVisible(`[data-section="story-header"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("nav: %v", err)
	}

	// AC1: responsive width — the story-detail main opts out of the global
	// centered max-width cap (max-width:none + percentage side padding), so the
	// content neither stretches edge-to-edge nor stays pinned to the 1280px cap.
	var maxWidth string
	if err := chromedp.Run(bctx, chromedp.Evaluate(
		`getComputedStyle(document.querySelector('main[data-page="story-detail"]')).maxWidth`, &maxWidth)); err != nil {
		t.Fatalf("read maxWidth: %v", err)
	}
	if maxWidth != "none" {
		t.Errorf("AC1: story-detail main max-width = %q, want \"none\" (responsive width not applied)", maxWidth)
	}

	// AC2: expanded header shows the story status + a live indicator.
	var statusText, liveState string
	if err := chromedp.Run(bctx,
		chromedp.Text(`[data-field="current-status"]`, &statusText, chromedp.ByQuery),
		chromedp.AttributeValue(`[data-field="live-indicator"]`, "data-state", &liveState, nil, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("read header: %v", err)
	}
	if strings.TrimSpace(statusText) != "in_progress" {
		t.Errorf("AC2: header status = %q, want \"in_progress\"", statusText)
	}
	if liveState != "live" {
		t.Errorf("AC2: live indicator state = %q, want \"live\"", liveState)
	}

	// AC3: description + ACs render as readable markdown (not raw text).
	var descHTML, acHTML string
	if err := chromedp.Run(bctx,
		chromedp.InnerHTML(`[data-field="story-description"]`, &descHTML, chromedp.ByQuery),
		chromedp.InnerHTML(`[data-field="story-acceptance"]`, &acHTML, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("read content: %v", err)
	}
	if !strings.Contains(descHTML, "<li>") || !strings.Contains(descHTML, "important") {
		t.Errorf("AC3: description not rendered as markdown: %q", descHTML)
	}
	if !strings.Contains(acHTML, "First criterion") {
		t.Errorf("AC3: acceptance criteria missing: %q", acHTML)
	}

	// AC2/AC4: expand/collapse toggles the readable body without a reload.
	displayOf := func(t *testing.T) string {
		t.Helper()
		var d string
		if err := chromedp.Run(bctx, chromedp.Evaluate(
			`getComputedStyle(document.querySelector('[data-section="story-body"]')).display`, &d)); err != nil {
			t.Fatalf("read body display: %v", err)
		}
		return d
	}
	if d := displayOf(t); d == "none" {
		t.Errorf("body should start expanded, display=%q", d)
	}
	if err := chromedp.Run(bctx,
		chromedp.Click(`[data-action="toggle-expand"]`, chromedp.ByQuery),
		chromedp.Sleep(200*time.Millisecond),
	); err != nil {
		t.Fatalf("click collapse: %v", err)
	}
	if d := displayOf(t); d != "none" {
		t.Errorf("body should be collapsed after toggle, display=%q", d)
	}
	if err := chromedp.Run(bctx,
		chromedp.Click(`[data-action="toggle-expand"]`, chromedp.ByQuery),
		chromedp.Sleep(200*time.Millisecond),
	); err != nil {
		t.Fatalf("click expand: %v", err)
	}
	if d := displayOf(t); d == "none" {
		t.Errorf("body should be expanded again after second toggle, display=%q", d)
	}
}
