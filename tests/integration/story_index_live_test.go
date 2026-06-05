//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/live"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/server"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
	"github.com/chromedp/chromedp"
)

// TestStoryIndexLiveUpdate drives a headless browser to prove the project
// story list updates live off the SSE bus (sty_8f69be8b): a status change made
// server-side (firing the documents_notify trigger → NOTIFY → /events) reaches
// the open page and the row's status pill changes in place, without a full
// reload (a page marker set in window survives) and without navigation.
func TestStoryIndexLiveUpdate(t *testing.T) {
	env := testbootstrap.SetUp(t)
	bg := context.Background()
	now := time.Date(2026, 5, 25, 16, 0, 0, 0, time.UTC)

	store := auth.New(env.DB)
	if err := store.DevSeed(bg); err != nil {
		t.Fatalf("dev seed: %v", err)
	}
	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	docStore := document.New(env.DB)
	verb.SetAuthStore(store)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	verb.SetDocumentStore(docStore)
	verb.SetLedgerStore(ledger.New(env.DB))
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
		verb.SetDocumentStore(nil)
		verb.SetLedgerStore(nil)
	})

	ws, err := wsStore.Create(bg, "", "live-index-ws", now)
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	pj, err := pjStore.Create(bg, project.CreateInput{WorkspaceID: ws.ID, Name: "live-index-pj"}, now)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	story, err := docStore.CreateStory(bg, document.CreateStoryInput{
		ProjectID: pj.ID, WorkspaceID: ws.ID, Title: "live index story",
	}, now)
	if err != nil {
		t.Fatalf("story: %v", err)
	}
	backlog := "backlog"
	if _, err := docStore.UpdateStory(bg, story.ID, document.UpdateStoryPatch{Status: &backlog}, now); err != nil {
		t.Fatalf("seed status backlog: %v", err)
	}

	hub := live.NewHub()
	listener, err := live.NewListener(env.DSN, hub)
	if err != nil {
		t.Fatalf("listener: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	liveScope := func(sctx context.Context, userID string) (live.Scope, error) {
		// DevSeed admin sees all topics.
		if u, err := store.GetUserByID(sctx, userID); err == nil && u != nil && u.Role == auth.RoleAdmin {
			return live.Scope{Admin: true}, nil
		}
		ids, err := wsStore.ListWorkspaceIDsForUser(sctx, userID)
		if err != nil {
			return live.Scope{}, err
		}
		return live.NewScope(false, ids), nil
	}

	handler := server.Build(server.Config{
		Store:     store,
		DevMode:   true,
		Live:      hub,
		LiveScope: liveScope,
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	ctx := newBrowserCtx(t)
	rowPill := `tr.story-row[data-id="` + story.ID + `"] .status-pill`

	// Login (dev admin), open the project, wait for the row, drop a marker.
	if err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL+"/login"),
		chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
		chromedp.Click(`button[data-action="dev-login-admin"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
		chromedp.Navigate(srv.URL+"/projects/"+pj.ID),
		chromedp.WaitVisible(rowPill, chromedp.ByQuery),
		// Marker survives only if the page is NOT reloaded by the refresh.
		chromedp.Evaluate(`window.__noReload = 'kept'`, nil),
	); err != nil {
		t.Fatalf("setup browser: %v", err)
	}

	var initial string
	if err := chromedp.Run(ctx, chromedp.Text(rowPill, &initial, chromedp.ByQuery)); err != nil {
		t.Fatalf("read initial pill: %v", err)
	}
	// The pill is CSS-uppercased; compare case-insensitively.
	if !strings.EqualFold(strings.TrimSpace(initial), "backlog") {
		t.Fatalf("initial status pill = %q, want backlog", initial)
	}

	// Server-side status change → documents_notify → NOTIFY → /events → live
	// refresh. Give the EventSource a beat to be established first.
	time.Sleep(500 * time.Millisecond)
	inProgress := "in_progress"
	if _, err := docStore.UpdateStory(bg, story.ID, document.UpdateStoryPatch{Status: &inProgress}, now.Add(time.Minute)); err != nil {
		t.Fatalf("update status: %v", err)
	}

	// The pill should flip live, no navigation.
	var ok bool
	if err := chromedp.Run(ctx, chromedp.Poll(
		`(()=>{const e=document.querySelector('`+rowPill+`');return !!e && e.textContent.trim()==='in_progress';})()`,
		&ok, chromedp.WithPollingTimeout(8*time.Second),
	)); err != nil {
		t.Fatalf("status pill did not update live: %v", err)
	}

	var marker, path string
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`window.__noReload || ''`, &marker),
		chromedp.Evaluate(`location.pathname`, &path),
	); err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if marker != "kept" {
		t.Fatal("page was reloaded (window marker lost) — live update should reconcile in place, not reload")
	}
	if path != "/projects/"+pj.ID {
		t.Fatalf("navigated away to %q; live update must not navigate", path)
	}

	// AC6 — an ADDED story reconciles in, exactly once (the tbody is fully
	// re-rendered each refresh, so a dupe would mean a reconcile bug).
	added, err := docStore.CreateStory(bg, document.CreateStoryInput{
		ProjectID: pj.ID, WorkspaceID: ws.ID, Title: "added live story",
	}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("create added story: %v", err)
	}
	var addOK bool
	if err := chromedp.Run(ctx, chromedp.Poll(
		`document.querySelectorAll('tr.story-row[data-id="`+added.ID+`"]').length === 1`,
		&addOK, chromedp.WithPollingTimeout(8*time.Second),
	)); err != nil {
		t.Fatalf("added story did not appear exactly once (dupe or missing): %v", err)
	}

	// AC6 — a REMOVED (soft-deleted) story drops from the page. document_delete
	// updates documents.status → fires the trigger; document_list then excludes
	// it so the refreshed fragment omits the row.
	delReq, _ := json.Marshal(verb.DocumentDeleteRequest{ID: story.ID})
	if _, err := verb.Dispatch(bg, "document_delete", delReq); err != nil {
		t.Fatalf("delete story: %v", err)
	}
	var removeOK bool
	if err := chromedp.Run(ctx, chromedp.Poll(
		`document.querySelectorAll('tr.story-row[data-id="`+story.ID+`"]').length === 0`,
		&removeOK, chromedp.WithPollingTimeout(8*time.Second),
	)); err != nil {
		t.Fatalf("removed story did not drop from the page: %v", err)
	}

	// The added row must still be present (and singular) after the remove
	// refresh — no skip/dupe across reconciles.
	var addStillOne bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		`document.querySelectorAll('tr.story-row[data-id="`+added.ID+`"]').length === 1`, &addStillOne,
	)); err != nil || !addStillOne {
		t.Fatalf("added row not present-exactly-once after remove reconcile (skip/dupe): err=%v", err)
	}
}
