//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/variable"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
	"github.com/chromedp/chromedp"
)

// TestStoryFilterURL exercises sty_a9864e07 — story panel filter
// persists to ?stories_q= so refresh + deep-link preserve the chip
// state.
func TestStoryFilterURL(t *testing.T) {
	skipInCI(t, "story panel chromedp session race fails on GH runner")
	env := testbootstrap.SetUpWithServer(t)

	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	docStore := document.New(env.DB)
	ledStore := ledger.New(env.DB)
	varStore := variable.New(env.DB)
	verb.SetAuthStore(env.Store)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	verb.SetDocumentStore(docStore)
	verb.SetLedgerStore(ledStore)
	verb.SetVariableStore(varStore)
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
		verb.SetDocumentStore(nil)
		verb.SetLedgerStore(nil)
		verb.SetVariableStore(nil)
	})

	ctx := context.Background()
	now := time.Date(2026, 5, 27, 16, 0, 0, 0, time.UTC)
	ws, err := wsStore.Create(ctx, "", "filter-url-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{
		WorkspaceID: ws.ID,
		Name:        "filter-url-project",
	}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	for _, s := range []struct {
		Name, Status string
		Tags         []string
	}{
		{"alpha", "backlog", []string{"area:portal"}},
		{"beta", "backlog", []string{"area:other"}},
		{"gamma", "ready", []string{"area:portal"}},
	} {
		st := s.Status
		tg := append([]string(nil), s.Tags...)
		req, _ := json.Marshal(verb.DocumentUpsertRequest{
			Type:      "story",
			ProjectID: pj.ID,
			Name:      s.Name,
			Status:    &st,
			Tags:      &tg,
		})
		if _, err := verb.Dispatch(ctx, "document_upsert", req); err != nil {
			t.Fatalf("seed %s: %v", s.Name, err)
		}
	}

	bctx := newBrowserCtx(t)

	// Login.
	if err := chromedp.Run(bctx,
		chromedp.Navigate(env.ServerURL+"/login"),
		chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
		chromedp.Click(`button[data-action="dev-login-admin"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("login: %v", err)
	}

	t.Run("typing filter writes to URL; reload restores it", func(t *testing.T) {
		// Navigate fresh.
		if err := chromedp.Run(bctx,
			chromedp.Navigate(env.ServerURL+"/projects/"+pj.ID),
			chromedp.WaitVisible(`[data-field="panel-stories-chip-status-open"]`, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("nav: %v", err)
		}
		// Set the query via direct value+input dispatch (mirrors the
		// existing panel test pattern; avoids per-character reactive
		// churn).
		if err := setStorySearch(bctx, "tags:area:portal"); err != nil {
			t.Fatalf("set search: %v", err)
		}
		if err := chromedp.Run(bctx,
			chromedp.WaitVisible(`[data-field="panel-stories-chip-tags-area:portal"]`, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("chip never rendered: %v", err)
		}
		// URL should now carry stories_q=tags:area:portal (URL-encoded).
		var currentURL string
		if err := chromedp.Run(bctx, chromedp.Location(&currentURL)); err != nil {
			t.Fatalf("location: %v", err)
		}
		u, _ := url.Parse(currentURL)
		if got := u.Query().Get("stories_q"); got != "tags:area:portal" {
			t.Errorf("URL stories_q: got %q want tags:area:portal", got)
		}

		// Reload — filter should restore.
		if err := chromedp.Run(bctx,
			chromedp.Reload(),
			chromedp.WaitVisible(`[data-field="panel-stories-chip-tags-area:portal"]`, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("reload: %v", err)
		}
		var searchValue string
		if err := chromedp.Run(bctx,
			chromedp.Value(`[data-field="panel-stories-search"]`, &searchValue, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("read input: %v", err)
		}
		if searchValue != "tags:area:portal" {
			t.Errorf("search input after reload: got %q want tags:area:portal", searchValue)
		}
		// Visible rows should be only alpha + gamma (both area:portal).
		if err := chromedp.Run(bctx, chromedp.Sleep(150*time.Millisecond)); err != nil {
			t.Fatalf("settle: %v", err)
		}
		visible, err := visibleRowTitles(bctx)
		if err != nil {
			t.Fatalf("visible: %v", err)
		}
		if !visible["alpha"] || !visible["gamma"] {
			t.Errorf("after reload, expected alpha+gamma visible; got %v", visible)
		}
		if visible["beta"] {
			t.Error("after reload, beta should be hidden (area:other excluded)")
		}
	})

	t.Run("deep-link with stories_q applies filter without interaction", func(t *testing.T) {
		// URL-encoded `tags:area:portal` = tags%3Aarea%3Aportal.
		if err := chromedp.Run(bctx,
			chromedp.Navigate(env.ServerURL+"/projects/"+pj.ID+"?stories_q=tags%3Aarea%3Aportal"),
			chromedp.WaitVisible(`[data-field="panel-stories-chip-tags-area:portal"]`, chromedp.ByQuery),
			chromedp.Sleep(150*time.Millisecond),
		); err != nil {
			t.Fatalf("deep-link: %v", err)
		}
		var searchValue string
		if err := chromedp.Run(bctx,
			chromedp.Value(`[data-field="panel-stories-search"]`, &searchValue, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("read input: %v", err)
		}
		if searchValue != "tags:area:portal" {
			t.Errorf("deep-link search: got %q want tags:area:portal", searchValue)
		}
		visible, err := visibleRowTitles(bctx)
		if err != nil {
			t.Fatalf("visible: %v", err)
		}
		if visible["beta"] {
			t.Error("deep-link filter not applied — beta visible")
		}
	})

	t.Run("clearing filter removes stories_q from URL", func(t *testing.T) {
		// Set, then clear.
		if err := setStorySearch(bctx, "tags:area:portal"); err != nil {
			t.Fatalf("set: %v", err)
		}
		if err := chromedp.Run(bctx, chromedp.Sleep(100*time.Millisecond)); err != nil {
			t.Fatalf("settle: %v", err)
		}
		if err := setStorySearch(bctx, ""); err != nil {
			t.Fatalf("clear: %v", err)
		}
		if err := chromedp.Run(bctx, chromedp.Sleep(100*time.Millisecond)); err != nil {
			t.Fatalf("settle: %v", err)
		}
		var currentURL string
		if err := chromedp.Run(bctx, chromedp.Location(&currentURL)); err != nil {
			t.Fatalf("location: %v", err)
		}
		u, _ := url.Parse(currentURL)
		if u.Query().Has("stories_q") {
			t.Errorf("URL still has stories_q after clear: %s", currentURL)
		}
	})

	t.Run("pagination params survive filter change", func(t *testing.T) {
		// Navigate with the UX pagination param (server-render-safe —
		// real cursor tokens are opaque base64 and pre-validated).
		// Filter mutation must not strip the page label.
		if err := chromedp.Run(bctx,
			chromedp.Navigate(env.ServerURL+"/projects/"+pj.ID+"?stories_page=2"),
			chromedp.WaitVisible(`[data-field="panel-stories-chip-status-open"]`, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("nav: %v", err)
		}
		if err := setStorySearch(bctx, "tags:area:portal"); err != nil {
			t.Fatalf("set: %v", err)
		}
		if err := chromedp.Run(bctx, chromedp.Sleep(100*time.Millisecond)); err != nil {
			t.Fatalf("settle: %v", err)
		}
		var currentURL string
		if err := chromedp.Run(bctx, chromedp.Location(&currentURL)); err != nil {
			t.Fatalf("location: %v", err)
		}
		u, _ := url.Parse(currentURL)
		if u.Query().Get("stories_page") != "2" {
			t.Errorf("stories_page not preserved: %s", currentURL)
		}
		if u.Query().Get("stories_q") != "tags:area:portal" {
			t.Errorf("stories_q not set: %s", currentURL)
		}
	})
}
