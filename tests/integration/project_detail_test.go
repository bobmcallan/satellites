//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/server"
	"github.com/bobmcallan/satellites/internal/variable"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestProjectDetailPanel exercises sty_f6911663 — V4-style story panel.
// Asserts the search input + chip strip + expandable rows + quick
// status-flip POST round-trip are present. The filter chips and row
// expansion are driven by Alpine client-side; tests assert the markup
// scaffolding is wired (data-field hooks the JS factory reads).
func TestProjectDetailPanel(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	authStore := auth.New(env.DB)
	if err := authStore.DevSeed(context.Background()); err != nil {
		t.Fatalf("dev seed: %v", err)
	}
	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	docStore := document.New(env.DB)
	ledStore := ledger.New(env.DB)
	verb.SetAuthStore(authStore)
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
	now := time.Date(2026, 5, 25, 16, 0, 0, 0, time.UTC)
	ws, err := wsStore.Create(ctx, "", "panel-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{
		WorkspaceID: ws.ID,
		Name:        "panel-project",
		Description: "the test project",
	}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	priorityHigh := "high"
	priorityLow := "low"
	tagsA := []string{"area:portal", "epic:test"}
	tagsB := []string{"area:other"}
	bodyA := "panel description body A"
	createReq, _ := json.Marshal(verb.DocumentUpsertRequest{
		Type:      "story",
		ProjectID: pj.ID,
		Name:      "wired story",
		Body:      bodyA,
		Priority:  &priorityHigh,
		Tags:      &tagsA,
	})
	createResp, err := verb.Dispatch(ctx, "document_upsert", createReq)
	if err != nil {
		t.Fatalf("create story 1: %v", err)
	}
	var storyA verb.DocumentUpsertResponse
	if err := json.Unmarshal(createResp, &storyA); err != nil {
		t.Fatalf("decode story 1: %v", err)
	}
	createReq2, _ := json.Marshal(verb.DocumentUpsertRequest{
		Type:      "story",
		ProjectID: pj.ID,
		Name:      "other story",
		Priority:  &priorityLow,
		Tags:      &tagsB,
	})
	createResp2, err := verb.Dispatch(ctx, "document_upsert", createReq2)
	if err != nil {
		t.Fatalf("create story 2: %v", err)
	}
	var storyB verb.DocumentUpsertResponse
	if err := json.Unmarshal(createResp2, &storyB); err != nil {
		t.Fatalf("decode story 2: %v", err)
	}

	sessions := auth.NewSessions([]byte("panel-test-secret"))
	handler := server.Build(server.Config{
		Store:    authStore,
		Sessions: sessions,
		DevMode:  true,
	})

	authedRequest := func(method, path string, body io.Reader) *http.Request {
		req := httptest.NewRequest(method, path, body)
		rec := httptest.NewRecorder()
		sessions.Issue(rec, "usr_dev_admin")
		for _, c := range rec.Result().Cookies() {
			req.AddCookie(c)
		}
		return req
	}
	get := func(t *testing.T, path string) (int, string) {
		t.Helper()
		req := authedRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		body, _ := io.ReadAll(rec.Result().Body)
		return rec.Code, string(body)
	}

	t.Run("panel renders search + chip strip + table scaffold", func(t *testing.T) {
		code, body := get(t, "/projects/"+pj.ID)
		if code != http.StatusOK {
			t.Fatalf("status %d, body=%s", code, body)
		}
		for _, want := range []string{
			`x-data="storyPanel"`,
			`data-field="panel-stories-search"`,
			`data-field="panel-stories-chips"`,
			`data-field="stories-table"`,
			`data-field="story-row"`,
			`data-field="story-detail"`,
			`data-field="story-status"`,
			`data-field="story-status-value"`,
			`data-status-rank=`,
			`data-field="story-description"`,
			`data-tab="documents"`,
			`data-field="story-documents"`,
			"wired story", "other story",
			"area:portal", "epic:test", "area:other",
			bodyA,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q", want)
			}
		}
		// Status is display-only (epic:dynamic-workflow-status order:2): the
		// editable status panel, the bulk status-set bar, and the selection
		// column are gone — no status-write control may leak onto the page.
		for _, gone := range []string{
			`data-field="story-status-buttons"`,
			`story-bulk-bar`,
			`status-button`,
			`class="col-select"`,
			`applyRowStatus`,
			`/api/stories/` + storyA.Document.ID + `/status`,
		} {
			if strings.Contains(body, gone) {
				t.Errorf("status-write control %q leaked into display-only page", gone)
			}
		}
		// No create/edit/delete forms leak onto the page.
		if strings.Contains(body, `data-form="projects-create"`) {
			t.Error("create form leaked into project detail page")
		}
		// Paginator only renders when total > page_size; with two
		// stories and the default page_size=50 it must be absent.
		if strings.Contains(body, `data-field="panel-stories-paginator"`) {
			t.Error("paginator rendered for a story count below page_size")
		}
	})

	t.Run("status is display-only — no write route", func(t *testing.T) {
		// The portal status-write route was removed (order:2): a POST to the
		// old endpoint must NOT succeed (route gone). Status moves only through
		// reviewer gates now.
		req := authedRequest(http.MethodPost, "/api/stories/"+storyA.Document.ID+"/status",
			bytes.NewReader([]byte(`{"status":"ready"}`)))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK {
			t.Fatalf("POST /api/stories/{id}/status returned 200 — the status-write route should be gone")
		}
	})

	t.Run("/projects card links to detail page", func(t *testing.T) {
		_, body := get(t, "/projects")
		want := `href="/projects/` + pj.ID + `"`
		if !strings.Contains(body, want) {
			t.Errorf("project list missing detail link %q", want)
		}
	})
}

// TestProjectDetailPagination exercises sty_5c41f316 — server-side
// cursor pagination with page size read from the system KV
// (sty_cc4c671a). Seeds 7 stories under a fresh project, sets
// stories.page_size=3 in the KV, then walks the prev/next links to
// verify each row appears on exactly one page and the back stack
// round-trips cleanly.
func TestProjectDetailPagination(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	authStore := auth.New(env.DB)
	if err := authStore.DevSeed(context.Background()); err != nil {
		t.Fatalf("dev seed: %v", err)
	}
	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	docStore := document.New(env.DB)
	ledStore := ledger.New(env.DB)
	varStore := variable.New(env.DB)
	verb.SetAuthStore(authStore)
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
	now := time.Date(2026, 5, 25, 16, 0, 0, 0, time.UTC)

	// Seed the page-size system KV — the handler reads this for every
	// /projects/{id} render, so the test must install it before the
	// first GET.
	if _, err := varStore.SeedSystem(ctx, "stories.page_size", "3", now); err != nil {
		t.Fatalf("seed page_size: %v", err)
	}

	ws, err := wsStore.Create(ctx, "", "pag-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{
		WorkspaceID: ws.ID,
		Name:        "pag-project",
		Description: "pagination project",
	}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	const want = 7
	ids := make([]string, 0, want)
	for i := 0; i < want; i++ {
		name := "pag-story-" + strconv.Itoa(i)
		req, _ := json.Marshal(verb.DocumentUpsertRequest{
			Type:      "story",
			ProjectID: pj.ID,
			Name:      name,
		})
		resp, err := verb.Dispatch(ctx, "document_upsert", req)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		var r verb.DocumentUpsertResponse
		if err := json.Unmarshal(resp, &r); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		ids = append(ids, r.Document.ID)
	}

	sessions := auth.NewSessions([]byte("pag-test-secret"))
	handler := server.Build(server.Config{
		Store:    authStore,
		Sessions: sessions,
		DevMode:  true,
	})
	get := func(path string) string {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		sessions.Issue(rec, "usr_dev_admin")
		for _, c := range rec.Result().Cookies() {
			req.AddCookie(c)
		}
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: %d", path, rec.Code)
		}
		body, _ := io.ReadAll(rec.Result().Body)
		return string(body)
	}
	countIDs := func(body string) []string {
		out := make([]string, 0, want)
		for _, id := range ids {
			if strings.Contains(body, `data-id="`+id+`"`) {
				out = append(out, id)
			}
		}
		return out
	}
	// extractLink pulls href="..." for the link with the given
	// data-field selector. The handler URL-encodes ampersands; we
	// decode them back so the link is a re-usable path for the next GET.
	extractLink := func(body, dataField string) string {
		t.Helper()
		idx := strings.Index(body, `data-field="`+dataField+`"`)
		if idx == -1 {
			return ""
		}
		// Walk back from idx to find href="...".
		seg := body[:idx]
		hrefStart := strings.LastIndex(seg, `href="`)
		if hrefStart == -1 {
			return ""
		}
		hrefStart += len(`href="`)
		hrefEnd := strings.Index(body[hrefStart:], `"`)
		if hrefEnd == -1 {
			return ""
		}
		href := body[hrefStart : hrefStart+hrefEnd]
		// html/template renders & as &amp; in attribute values.
		return strings.ReplaceAll(href, "&amp;", "&")
	}

	t.Run("page 1: no prev, next present, page indicator with total", func(t *testing.T) {
		body := get("/projects/" + pj.ID)
		for _, w := range []string{
			`data-field="panel-stories-paginator"`,
			`data-field="panel-stories-next"`,
			`data-field="panel-stories-page-indicator"`,
			// sty_975afe24: total is back via document_count. 7 stories
			// at page_size=3 → 3 pages.
			"page 1 of 3",
			// sty_975afe24: shown/total chip in the panel header.
			`data-field="stories-count-indicator"`,
		} {
			if !strings.Contains(body, w) {
				t.Errorf("page 1 missing %q", w)
			}
		}
		// Prev is disabled on page 1 (rendered as span, no data-field
		// because the active anchor element isn't present).
		if strings.Contains(body, `data-field="panel-stories-prev"`) {
			t.Error("page 1 should not render an active prev link")
		}
	})

	t.Run("walking next reaches every row exactly once", func(t *testing.T) {
		seen := make(map[string]int, want)
		path := "/projects/" + pj.ID
		for visited := 0; visited < 5; visited++ { // 3 pages + safety
			body := get(path)
			for _, id := range countIDs(body) {
				seen[id]++
			}
			next := extractLink(body, "panel-stories-next")
			if next == "" {
				break // no more pages
			}
			path = next
		}
		if len(seen) != want {
			t.Errorf("union of forward walk: got %d unique rows, want %d", len(seen), want)
		}
		for id, n := range seen {
			if n != 1 {
				t.Errorf("row %s appeared on %d pages during forward walk, want 1", id, n)
			}
		}
	})

	t.Run("last page disables next, prev present", func(t *testing.T) {
		// Walk forward to the last page by following next links until
		// no next remains.
		path := "/projects/" + pj.ID
		var body string
		for i := 0; i < 5; i++ {
			body = get(path)
			next := extractLink(body, "panel-stories-next")
			if next == "" {
				break
			}
			path = next
		}
		if strings.Contains(body, `data-field="panel-stories-next"`) {
			t.Error("last page should not render an active next link")
		}
		if !strings.Contains(body, `data-field="panel-stories-prev"`) {
			t.Error("last page should render an active prev link")
		}
	})

	t.Run("prev round-trips back to page 1", func(t *testing.T) {
		// Walk forward to page 3, then walk back via prev — should
		// land on a page with no active prev link (page 1 again).
		path := "/projects/" + pj.ID
		for i := 0; i < 2; i++ {
			body := get(path)
			next := extractLink(body, "panel-stories-next")
			if next == "" {
				t.Fatalf("forward walk step %d: no next link", i)
			}
			path = next
		}
		// We're now on page 3 (the last page). Walk back twice.
		for i := 0; i < 2; i++ {
			body := get(path)
			prev := extractLink(body, "panel-stories-prev")
			if prev == "" {
				t.Fatalf("backward walk step %d: no prev link", i)
			}
			path = prev
		}
		// path should now be page 1 (no active prev link).
		body := get(path)
		if strings.Contains(body, `data-field="panel-stories-prev"`) {
			t.Error("after two prev clicks from page 3, expected to land on page 1 (no active prev link)")
		}
		if !strings.Contains(body, "page 1") {
			t.Error("after back-walk, expected page 1 indicator")
		}
	})
}
