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
			`data-field="story-status-buttons"`,
			`data-field="story-description"`,
			`data-field="story-acceptance"`,
			`data-field="story-bulk-bar"`,
			`data-field="story-bulk-target"`,
			`data-field="story-bulk-apply"`,
			`data-field="story-bulk-clear"`,
			`data-field="story-row-select"`,
			`class="col-select"`,
			"wired story", "other story",
			"area:portal", "epic:test", "area:other",
			bodyA,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q", want)
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

	t.Run("status buttons render one per enum value", func(t *testing.T) {
		_, body := get(t, "/projects/"+pj.ID)
		for _, want := range []string{
			`data-field="story-status-button-backlog"`,
			`data-field="story-status-button-ready"`,
			`data-field="story-status-button-in_progress"`,
			`data-field="story-status-button-review"`,
			`data-field="story-status-button-done"`,
			`data-field="story-status-button-cancelled"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q", want)
			}
		}
	})

	t.Run("status POST round-trips through document_upsert", func(t *testing.T) {
		payload := bytes.NewReader([]byte(`{"status":"ready"}`))
		req := authedRequest(http.MethodPost, "/api/stories/"+storyA.Document.ID+"/status", payload)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			body, _ := io.ReadAll(rec.Result().Body)
			t.Fatalf("status POST: code=%d body=%s", rec.Code, string(body))
		}

		// Verify the new status survives a re-render.
		_, body := get(t, "/projects/"+pj.ID)
		want := `<tr class="story-row"
              data-id="` + storyA.Document.ID + `"
              data-status="ready"`
		if !strings.Contains(body, want) {
			t.Errorf("story row did not pick up new status; body excerpt missing %q", want)
		}
	})

	t.Run("bulk status fan-out POSTs both round-trip", func(t *testing.T) {
		// Mirrors the client-side Promise.all that storyPanel.applySelectionStatus
		// issues: one POST per selected id, all hitting /api/stories/{id}/status.
		for _, id := range []string{storyA.Document.ID, storyB.Document.ID} {
			payload := bytes.NewReader([]byte(`{"status":"done"}`))
			req := authedRequest(http.MethodPost, "/api/stories/"+id+"/status", payload)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				body, _ := io.ReadAll(rec.Result().Body)
				t.Fatalf("bulk POST id=%s: code=%d body=%s", id, rec.Code, string(body))
			}
		}

		// Verify both rows pick up the new status on re-render — what a
		// reload after a bulk apply would show.
		_, body := get(t, "/projects/"+pj.ID)
		for _, id := range []string{storyA.Document.ID, storyB.Document.ID} {
			want := `data-id="` + id + `"
              data-status="done"`
			if !strings.Contains(body, want) {
				t.Errorf("bulk apply did not flip %s to done; body excerpt missing %q", id, want)
			}
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

// TestProjectDetailPagination exercises sty_4e6dcbec — page-based
// paginator below the stories panel. Seeds 7 stories under a fresh
// project; walks page 1/2/3 at page_size=3 and asserts each row
// appears exactly once across the three pages.
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

	t.Run("paginator renders at total > page_size", func(t *testing.T) {
		body := get("/projects/" + pj.ID + "?stories_page=1&stories_page_size=3")
		for _, want := range []string{
			`data-field="panel-stories-paginator"`,
			`data-field="panel-stories-next"`,
			`data-field="panel-stories-page-indicator"`,
			"page 1 of 3",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("page 1: missing %q", want)
			}
		}
		// Prev is disabled on page 1 (span, not anchor).
		if strings.Contains(body, `data-field="panel-stories-prev"`) {
			t.Error("page 1 should not render an active prev link")
		}
	})

	t.Run("pages partition the row set", func(t *testing.T) {
		seen := make(map[string]int, want)
		for page := 1; page <= 3; page++ {
			body := get("/projects/" + pj.ID + "?stories_page=" + strconv.Itoa(page) + "&stories_page_size=3")
			for _, id := range countIDs(body) {
				seen[id]++
			}
		}
		if len(seen) != want {
			t.Errorf("union of pages: got %d unique rows, want %d", len(seen), want)
		}
		for id, n := range seen {
			if n != 1 {
				t.Errorf("row %s appeared on %d pages, want exactly 1", id, n)
			}
		}
	})

	t.Run("last page disables next", func(t *testing.T) {
		body := get("/projects/" + pj.ID + "?stories_page=3&stories_page_size=3")
		if !strings.Contains(body, "page 3 of 3") {
			t.Error("page 3 indicator missing")
		}
		if !strings.Contains(body, `data-field="panel-stories-prev"`) {
			t.Error("page 3 should render an active prev link")
		}
		if strings.Contains(body, `data-field="panel-stories-next"`) {
			t.Error("page 3 should not render an active next link")
		}
	})
}
