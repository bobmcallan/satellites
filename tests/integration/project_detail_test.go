//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/server"
	"github.com/bobmcallan/satellites/internal/story"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestProjectDetailPanel exercises sty_045b00e3:
//   - GET /projects/{id} renders stories panel
//   - Table columns + tag chips + sort links present
//   - Row click reveals summary + ledger sections
//   - Tag filter URL toggles the active set
//   - No mutation forms on the page
func TestProjectDetailPanel(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	authStore := auth.New(env.DB)
	if err := authStore.DevSeed(context.Background()); err != nil {
		t.Fatalf("dev seed: %v", err)
	}
	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	stStore := story.New(env.DB)
	ledStore := ledger.New(env.DB)
	verb.SetAuthStore(authStore)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	verb.SetStoryStore(stStore)
	verb.SetLedgerStore(ledStore)
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
		verb.SetStoryStore(nil)
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

	// Two stories: one with a unique tag we'll filter on, one without.
	createReq, _ := json.Marshal(verb.StoryCreateRequest{
		ProjectID: pj.ID,
		Title:     "wired story",
		Priority:  "high",
		Tags:      []string{"area:portal", "epic:test"},
	})
	if _, err := verb.Dispatch(ctx, "story_create", createReq); err != nil {
		t.Fatalf("create story 1: %v", err)
	}
	createReq2, _ := json.Marshal(verb.StoryCreateRequest{
		ProjectID: pj.ID,
		Title:     "other story",
		Priority:  "low",
		Tags:      []string{"area:other"},
	})
	if _, err := verb.Dispatch(ctx, "story_create", createReq2); err != nil {
		t.Fatalf("create story 2: %v", err)
	}

	sessions := auth.NewSessions([]byte("panel-test-secret"))
	handler := server.Build(server.Config{
		Store:    authStore,
		Sessions: sessions,
		DevMode:  true,
	})

	get := func(t *testing.T, path string) (int, string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		// Need a real user_id from DevSeed.
		sessions.Issue(rec, "usr_dev_admin")
		for _, c := range rec.Result().Cookies() {
			req.AddCookie(c)
		}
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		body, _ := io.ReadAll(rec.Result().Body)
		return rec.Code, string(body)
	}

	t.Run("renders stories table + columns", func(t *testing.T) {
		code, body := get(t, "/projects/"+pj.ID)
		if code != http.StatusOK {
			t.Fatalf("status %d, body=%s", code, body)
		}
		for _, want := range []string{
			`data-field="stories-table"`,
			`data-sort="id"`, `data-sort="title"`, `data-sort="status"`,
			`data-sort="priority"`, `data-sort="updated_at"`,
			"wired story", "other story",
			"area:portal", "epic:test", "area:other",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q", want)
			}
		}
		// AC #6: no create/edit/delete forms on this page.
		if strings.Contains(body, `data-form="projects-create"`) {
			t.Error("create form leaked into project detail page")
		}
	})

	t.Run("tag filter narrows the table", func(t *testing.T) {
		code, body := get(t, "/projects/"+pj.ID+"?tag=area:portal")
		if code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
		if !strings.Contains(body, "wired story") {
			t.Error("tag-filtered list missing matching story")
		}
		if strings.Contains(body, "other story") {
			t.Error("tag-filtered list leaked non-matching story")
		}
		if !strings.Contains(body, `data-field="active-filters"`) {
			t.Error("active-filters chip section missing")
		}
	})

	t.Run("expanded row exposes summary + ledger placeholders", func(t *testing.T) {
		_, body := get(t, "/projects/"+pj.ID)
		for _, want := range []string{
			`data-field="story-detail"`,
			`data-field="story-summary"`,
			`data-field="story-ledger"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q", want)
			}
		}
		// Story_created ledger entry from story_create should render.
		if !strings.Contains(body, "story_created") {
			t.Error("body missing story_created ledger entry")
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
