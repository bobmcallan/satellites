//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/server"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
	"github.com/chromedp/chromedp"
)

// TestStoryCategoryChip exercises sty_7772f005: each row renders its category as
// a clickable chip (category:<value>), and clicking it filters the list by that
// category (server-side). HTML assertion for the render; chromedp for the click.
func TestStoryCategoryChip(t *testing.T) {
	env := testbootstrap.SetUp(t)
	bg := context.Background()
	now := time.Date(2026, 5, 25, 16, 0, 0, 0, time.UTC)

	store := auth.New(env.DB)
	if err := store.DevSeed(bg); err != nil {
		t.Fatalf("dev seed: %v", err)
	}
	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	verb.SetAuthStore(store)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	verb.SetDocumentStore(document.New(env.DB))
	verb.SetLedgerStore(ledger.New(env.DB))
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
		verb.SetDocumentStore(nil)
		verb.SetLedgerStore(nil)
	})

	ws, _ := wsStore.Create(bg, "", "cat-ws", now)
	pj, err := pjStore.Create(bg, project.CreateInput{WorkspaceID: ws.ID, Name: "cat-pj"}, now)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	seed := func(name, category string) string {
		cat, st := category, "backlog"
		req, _ := json.Marshal(verb.DocumentUpsertRequest{
			Type: "story", ProjectID: pj.ID, Name: name, Category: &cat, Status: &st,
		})
		raw, err := verb.Dispatch(bg, "document_upsert", req)
		if err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		var resp verb.DocumentUpsertResponse
		_ = json.Unmarshal(raw, &resp)
		return resp.Document.ID
	}
	featureID := seed("feature story", "feature")
	bugID := seed("bug story", "bug")

	sessions := auth.NewSessions([]byte("cat-chip-secret-0123456789abcdef"))
	srv := httptest.NewServer(server.Build(server.Config{Store: store, Sessions: sessions, DevMode: true}))
	t.Cleanup(srv.Close)

	// HTML render assertion (no browser): both category chips present.
	t.Run("rows render a category chip", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/projects/"+pj.ID, nil)
		rec := httptest.NewRecorder()
		sessions.Issue(rec, "usr_dev_admin")
		for _, c := range rec.Result().Cookies() {
			req.AddCookie(c)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		defer resp.Body.Close()
		buf := new(strings.Builder)
		tmp := make([]byte, 4096)
		for {
			n, e := resp.Body.Read(tmp)
			buf.Write(tmp[:n])
			if e != nil {
				break
			}
		}
		body := buf.String()
		for _, want := range []string{
			`class="category-chip is-clickable"`,
			`data-category="feature"`, `category:feature`,
			`data-category="bug"`, `category:bug`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q", want)
			}
		}
	})

	// chromedp: clicking the feature chip filters to category:feature server-side.
	t.Run("clicking the chip filters by category", func(t *testing.T) {
		ctx := newBrowserCtx(t)
		featureChip := `button.category-chip[data-category="feature"]`
		bugRow := `tr.story-row[data-id="` + bugID + `"]`
		featureRow := `tr.story-row[data-id="` + featureID + `"]`

		// Load + click the feature chip. applyToServer navigates the page
		// (full reload with ?stories_q=category:feature).
		if err := chromedp.Run(ctx,
			chromedp.Navigate(srv.URL+"/login"),
			chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
			chromedp.Click(`button[data-action="dev-login-admin"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
			chromedp.Navigate(srv.URL+"/projects/"+pj.ID),
			chromedp.WaitVisible(bugRow, chromedp.ByQuery), // both rows present pre-filter
			chromedp.Click(featureChip, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("click category chip: %v", err)
		}

		// After the navigation settles: feature row present, bug row gone,
		// query carries category:feature. WaitVisible re-attaches to the new
		// page's context (Poll would race the navigation's context teardown).
		var search string
		var bugPresent bool
		if err := chromedp.Run(ctx,
			chromedp.WaitVisible(featureRow, chromedp.ByQuery),
			chromedp.Evaluate(`location.search`, &search),
			chromedp.Evaluate(`!!document.querySelector('`+bugRow+`')`, &bugPresent),
		); err != nil {
			t.Fatalf("post-filter assertions: %v", err)
		}
		if !strings.Contains(search, "category") {
			t.Fatalf("query did not gain a category filter; location.search=%q", search)
		}
		if bugPresent {
			t.Fatal("bug row still present after filtering to category:feature")
		}
	})
}
