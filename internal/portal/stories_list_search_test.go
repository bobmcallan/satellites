package portal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/story"
)

// renderStoriesList boots a portal, signs in alice, creates her project,
// optionally seeds rows, and returns the response from
// GET /projects/{id}/stories?{query}.
func renderStoriesList(t *testing.T, query string, seed func(ctx context.Context, projectID string, stories *story.MemoryStore)) *httptest.ResponseRecorder {
	t.Helper()
	p, users, sessions, projects, _, stories, _, _, _ := newTestPortalWithContracts(t, &config.Config{Env: "dev"})
	mux := http.NewServeMux()
	p.Register(mux)

	alice := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(alice)

	ctx := context.Background()
	proj, err := projects.Create(ctx, alice.ID, "", "alpha", time.Now().UTC())
	if err != nil {
		t.Fatalf("project create: %v", err)
	}
	if seed != nil {
		seed(ctx, proj.ID, stories)
	}

	sess, _ := sessions.Create(alice.ID, auth.DefaultSessionTTL)
	url := "/projects/" + proj.ID + "/stories"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestStoriesList_RendersSearchInputAndChips covers AC2 + AC3 of
// story_59b11d8c: the search input, status/priority/category filter
// chips, and the panel-level refresh button render on every load.
func TestStoriesList_RendersSearchInputAndChips(t *testing.T) {
	t.Parallel()
	rec := renderStoriesList(t, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`data-testid="stories-panel"`,
		`data-testid="stories-search-form"`,
		`data-testid="stories-search-input"`,
		`name="q"`,
		`data-testid="stories-filter-chips"`,
		`data-testid="filter-chip-status"`,
		`data-testid="filter-chip-priority"`,
		`data-testid="filter-chip-category"`,
		`data-testid="stories-panel-refresh"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

// TestStoriesList_SortableColumnsRendered covers the column shape from
// the V3 reference: id-checkbox + id + title + category + priority +
// status + pts + created + updated. Each column header (except select
// and pts) carries a sort link.
func TestStoriesList_SortableColumnsRendered(t *testing.T) {
	t.Parallel()
	rec := renderStoriesList(t, "", func(ctx context.Context, projectID string, stories *story.MemoryStore) {
		_, _ = stories.Create(ctx, story.Story{
			ProjectID: projectID, Title: "seeded", Status: "backlog", Priority: "medium", Category: "feature",
		}, time.Now().UTC())
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`data-testid="stories-table"`,
		`data-testid="col-id"`,
		`data-testid="col-title"`,
		`data-testid="col-category"`,
		`data-testid="col-priority"`,
		`data-testid="col-status"`,
		`data-testid="col-created"`,
		`data-testid="col-updated"`,
		`<th class="col-pts">PTS</th>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing column marker %q", want)
		}
	}
}

// TestStoriesList_QFilterMatchesTitleSubstring covers the bare-term
// branch of AC3: ?q=needle returns only stories whose title or
// description contain "needle".
func TestStoriesList_QFilterMatchesTitleSubstring(t *testing.T) {
	t.Parallel()
	rec := renderStoriesList(t, "q=needle", func(ctx context.Context, projectID string, stories *story.MemoryStore) {
		now := time.Now().UTC()
		_, _ = stories.Create(ctx, story.Story{ProjectID: projectID, Title: "alpha story", Description: "first", Status: "backlog"}, now)
		_, _ = stories.Create(ctx, story.Story{ProjectID: projectID, Title: "beta needle here", Description: "second", Status: "backlog"}, now)
		_, _ = stories.Create(ctx, story.Story{ProjectID: projectID, Title: "gamma", Description: "matches needle in body", Status: "backlog"}, now)
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "beta needle here") {
		t.Errorf("body missing matching title")
	}
	if !strings.Contains(body, "gamma") {
		t.Errorf("body missing matching description row")
	}
	if strings.Contains(body, "alpha story") {
		t.Errorf("body still includes non-matching story 'alpha story'")
	}
}

// TestStoriesList_StatusPriorityCategoryFilters covers AC3's typed
// filter keys: ?status, ?priority, ?category each restrict the result
// set independently.
func TestStoriesList_StatusPriorityCategoryFilters(t *testing.T) {
	t.Parallel()
	seed := func(ctx context.Context, projectID string, stories *story.MemoryStore) {
		now := time.Now().UTC()
		if _, err := stories.Create(ctx, story.Story{ProjectID: projectID, Title: "backlog-high-bug", Status: story.StatusBacklog, Priority: "high", Category: "bug"}, now); err != nil {
			t.Fatalf("seed: %v", err)
		}
		if _, err := stories.Create(ctx, story.Story{ProjectID: projectID, Title: "backlog-low-feature", Status: story.StatusBacklog, Priority: "low", Category: "feature"}, now); err != nil {
			t.Fatalf("seed: %v", err)
		}
		if _, err := stories.Create(ctx, story.Story{ProjectID: projectID, Title: "ready-high-feature", Status: story.StatusReady, Priority: "high", Category: "feature"}, now); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	cases := []struct {
		name    string
		query   string
		want    []string
		notWant []string
	}{
		{name: "status filter", query: "status=backlog", want: []string{"backlog-high-bug", "backlog-low-feature"}, notWant: []string{"ready-high-feature"}},
		{name: "priority filter", query: "priority=high", want: []string{"backlog-high-bug", "ready-high-feature"}, notWant: []string{"backlog-low-feature"}},
		{name: "category filter", query: "category=bug", want: []string{"backlog-high-bug"}, notWant: []string{"backlog-low-feature", "ready-high-feature"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := renderStoriesList(t, tc.query, seed)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d", rec.Code)
			}
			body := rec.Body.String()
			for _, w := range tc.want {
				if !strings.Contains(body, w) {
					t.Errorf("body missing %q", w)
				}
			}
			for _, n := range tc.notWant {
				if strings.Contains(body, n) {
					t.Errorf("body should not contain %q", n)
				}
			}
		})
	}
}

// TestStoriesList_CountFormatShowsFilteredAndTotal covers AC4: the
// stories panel header renders "(n / total)" where n is the filtered
// count and total is the unfiltered count.
func TestStoriesList_CountFormatShowsFilteredAndTotal(t *testing.T) {
	t.Parallel()
	rec := renderStoriesList(t, "q=match", func(ctx context.Context, projectID string, stories *story.MemoryStore) {
		now := time.Now().UTC()
		_, _ = stories.Create(ctx, story.Story{ProjectID: projectID, Title: "match-1", Status: "backlog"}, now)
		_, _ = stories.Create(ctx, story.Story{ProjectID: projectID, Title: "skip-1", Status: "backlog"}, now)
		_, _ = stories.Create(ctx, story.Story{ProjectID: projectID, Title: "skip-2", Status: "backlog"}, now)
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "(1 / 3)") {
		t.Errorf("body missing count format '(1 / 3)' — got body fragment around stories-count: %s", extractAround(body, `data-testid="stories-count"`, 200))
	}
}

// extractAround returns ~width bytes of text either side of marker for
// failure-message context. Returns empty string when the marker is
// missing.
func extractAround(body, marker string, width int) string {
	idx := strings.Index(body, marker)
	if idx < 0 {
		return ""
	}
	start := idx - width
	if start < 0 {
		start = 0
	}
	end := idx + width
	if end > len(body) {
		end = len(body)
	}
	return body[start:end]
}
