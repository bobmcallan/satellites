//go:build integration

package integration_test

import (
	"context"
	"net/http"
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

// TestStoryContentLiveUpdate proves the story detail / QA process view updates
// live off the shared SSE bus (sty_96cc0ade): a server-side status transition
// fires a story-scoped NOTIFY, the page refetches the trace fragment, and the
// status pill changes in place with no reload. Also asserts the retired
// per-story SSE endpoint (/api/stories/{id}/events) no longer serves a stream.
func TestStoryContentLiveUpdate(t *testing.T) {
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

	ws, err := wsStore.Create(bg, "", "content-ws", now)
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	pj, err := pjStore.Create(bg, project.CreateInput{WorkspaceID: ws.ID, Name: "content-pj"}, now)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	story, err := docStore.CreateStory(bg, document.CreateStoryInput{
		ProjectID: pj.ID, WorkspaceID: ws.ID, Title: "content live story",
	}, now)
	if err != nil {
		t.Fatalf("story: %v", err)
	}
	backlog := "backlog"
	if _, err := docStore.UpdateStory(bg, story.ID, document.UpdateStoryPatch{Status: &backlog}, now); err != nil {
		t.Fatalf("seed backlog: %v", err)
	}

	hub := live.NewHub()
	listener, err := live.NewListener(env.DSN, hub)
	if err != nil {
		t.Fatalf("listener: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	liveScope := func(sctx context.Context, userID string) (live.Scope, error) {
		if u, err := store.GetUserByID(sctx, userID); err == nil && u != nil && u.Role == auth.RoleAdmin {
			return live.Scope{Admin: true}, nil
		}
		ids, err := wsStore.ListWorkspaceIDsForUser(sctx, userID)
		if err != nil {
			return live.Scope{}, err
		}
		return live.NewScope(false, ids), nil
	}

	handler := server.Build(server.Config{Store: store, DevMode: true, Live: hub, LiveScope: liveScope})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// The retired per-story SSE endpoint must no longer serve a stream.
	if resp, err := http.Get(srv.URL + "/api/stories/" + story.ID + "/events"); err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK &&
			strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
			t.Fatalf("retired /api/stories/{id}/events still serves an SSE stream (status %d)", resp.StatusCode)
		}
	}

	ctx := newBrowserCtx(t)
	pill := `[data-field="current-status"]`

	if err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL+"/login"),
		chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
		chromedp.Click(`button[data-action="dev-login-admin"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
		chromedp.Navigate(srv.URL+"/stories/"+story.ID),
		chromedp.WaitVisible(pill, chromedp.ByQuery),
		chromedp.Evaluate(`window.__noReload = 'kept'`, nil),
	); err != nil {
		t.Fatalf("setup browser: %v", err)
	}

	var initial string
	if err := chromedp.Run(ctx, chromedp.Text(pill, &initial, chromedp.ByQuery)); err != nil {
		t.Fatalf("read initial status: %v", err)
	}
	if !strings.EqualFold(strings.TrimSpace(initial), "backlog") {
		t.Fatalf("initial status = %q, want backlog", initial)
	}

	time.Sleep(500 * time.Millisecond) // let the EventSource establish
	inProgress := "in_progress"
	if _, err := docStore.UpdateStory(bg, story.ID, document.UpdateStoryPatch{Status: &inProgress}, now.Add(time.Minute)); err != nil {
		t.Fatalf("update status: %v", err)
	}

	var ok bool
	if err := chromedp.Run(ctx, chromedp.Poll(
		`(()=>{const e=document.querySelector('`+pill+`');return !!e && e.textContent.trim()==='in_progress';})()`,
		&ok, chromedp.WithPollingTimeout(8*time.Second),
	)); err != nil {
		t.Fatalf("status pill did not update live: %v", err)
	}

	var marker string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`window.__noReload || ''`, &marker)); err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if marker != "kept" {
		t.Fatal("page reloaded (window marker lost) — content update should swap the region in place")
	}
}
