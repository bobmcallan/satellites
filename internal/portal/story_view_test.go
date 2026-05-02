package portal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/story"
)

// renderStoryDetail drives a GET /projects/{pid}/stories/{sid} request
// against p and returns the response.
func renderStoryDetail(t *testing.T, p *Portal, projID, storyID, sessionCookie string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	p.Register(mux)
	req := httptest.NewRequest(http.MethodGet, "/projects/"+projID+"/stories/"+storyID, nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sessionCookie})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// renderComposite drives a GET /api/stories/{sid}/composite and returns
// the parsed composite + the response.
func renderComposite(t *testing.T, p *Portal, storyID, sessionCookie string) (*httptest.ResponseRecorder, storyComposite) {
	t.Helper()
	mux := http.NewServeMux()
	p.Register(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/stories/"+storyID+"/composite", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sessionCookie})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var c storyComposite
	if rec.Code == http.StatusOK {
		_ = json.Unmarshal(rec.Body.Bytes(), &c)
	}
	return rec, c
}

func TestStoryView_EmptyPanelsRender(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, _, stories := newTestPortal(t, &config.Config{Env: "dev"})
	ctx := context.Background()
	now := time.Now().UTC()
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	proj, _ := projects.Create(ctx, user.ID, "", "alpha", now)
	s, _ := stories.Create(ctx, story.Story{
		ProjectID:          proj.ID,
		Title:              "empty",
		Description:        "no source tags",
		AcceptanceCriteria: "ac",
		Status:             "in_progress",
		Priority:           "low",
		Category:           "feature",
		CreatedBy:          user.ID,
	}, now)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	rec := renderStoryDetail(t, p, proj.ID, s.ID, sess.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`data-testid="delivery-strip"`,
		`data-testid="scope-panel"`,
		`data-testid="source-docs-panel"`,
		`data-testid="ci-walk-link-panel"`,
		`data-testid="verdict-panel"`,
		`data-testid="repo-provenance-panel"`,
		`data-testid="excerpts-panel"`,
		`No source documents`,
		`No commits linked yet`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q in empty-state render", want)
		}
	}
}

func TestStoryView_SourceDocsFromTags(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, _, stories := newTestPortal(t, &config.Config{Env: "dev"})
	ctx := context.Background()
	now := time.Now().UTC()
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	proj, _ := projects.Create(ctx, user.ID, "", "alpha", now)
	s, _ := stories.Create(ctx, story.Story{
		ProjectID:          proj.ID,
		Title:              "with sources",
		Description:        "d",
		AcceptanceCriteria: "ac",
		Status:             "in_progress",
		Priority:           "high",
		Category:           "feature",
		Tags:               []string{"source:ui-design.md#story-view", "source:architecture.md", "epic:portal-views"},
		CreatedBy:          user.ID,
	}, now)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	rec := renderStoryDetail(t, p, proj.ID, s.ID, sess.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`source:ui-design.md#story-view`,
		`ui-design.md §story-view`,
		`source:architecture.md`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing source doc %q", want)
		}
	}
	if strings.Contains(body, "No source documents") {
		t.Errorf("body must not show empty state when source: tags exist")
	}
}

// TestStoryView_LinksToWalkPage verifies sty_3132035b — the story
// detail page links to /stories/{id}/walk where the contract walk
// content now lives. The prior TestStoryView_CITimelinePopulated test
// asserted CI rows on the story detail itself; that surface was
// retired and its assertions now live in story_walk_view_test.go.
func TestStoryView_LinksToWalkPage(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, _, stories := newTestPortal(t, &config.Config{Env: "dev"})
	ctx := context.Background()
	now := time.Now().UTC()
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	proj, _ := projects.Create(ctx, user.ID, "", "alpha", now)
	s, _ := stories.Create(ctx, story.Story{
		ProjectID: proj.ID,
		Title:     "with CIs",
		Status:    "in_progress",
		Priority:  "high",
		Category:  "feature",
		CreatedBy: user.ID,
	}, now)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)
	rec := renderStoryDetail(t, p, proj.ID, s.ID, sess.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`data-testid="ci-walk-link-panel"`,
		`href="/stories/` + s.ID + `/walk"`,
		`View contract walk`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestStoryView_VerdictPanelReadsLedger(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, ledgerStore, stories := newTestPortal(t, &config.Config{Env: "dev"})
	ctx := context.Background()
	now := time.Now().UTC()
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	proj, _ := projects.Create(ctx, user.ID, "", "alpha", now)
	s, _ := stories.Create(ctx, story.Story{
		ProjectID: proj.ID,
		Title:     "verdict story",
		Status:    "in_progress",
		Priority:  "high",
		Category:  "feature",
		CreatedBy: user.ID,
	}, now)
	storyRef := s.ID
	verdictPayload, _ := json.Marshal(map[string]any{
		"verdict":   "approved",
		"score":     5,
		"reasoning": "all ACs satisfied",
	})
	_, _ = ledgerStore.Append(ctx, ledger.LedgerEntry{
		ProjectID:  proj.ID,
		StoryID:    &storyRef,
		Type:       ledger.TypeVerdict,
		Tags:       []string{"kind:verdict", "phase:story_close"},
		Content:    "approved",
		Structured: verdictPayload,
		Durability: ledger.DurabilityDurable,
		SourceType: ledger.SourceSystem,
		Status:     ledger.StatusActive,
		CreatedBy:  "system",
	}, now)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	rec := renderStoryDetail(t, p, proj.ID, s.ID, sess.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// Empty-state copy is always emitted inside Alpine <template x-if> blocks
	// (Alpine hides them at runtime). The noscript SSR marker is the
	// reliable signal that the SSR payload contains no rows.
	if strings.Contains(body, `data-testid="verdict-empty-ssr"`) {
		t.Errorf("verdict empty-state SSR marker present when verdict row exists")
	}
	// Verdict row content also lands in the excerpts SSR table.
	if !strings.Contains(body, "approved") {
		t.Errorf("verdict content missing from body")
	}
	// Delivery strip should pick up resolution=delivered for an approved
	// story_close verdict.
	if !strings.Contains(body, `class="resolution-pill">delivered`) {
		t.Errorf("delivery strip missing delivered resolution; body = %s", body)
	}
}

func TestStoryView_CompositeJSONEndpoint(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, _, stories := newTestPortal(t, &config.Config{Env: "dev"})
	ctx := context.Background()
	now := time.Now().UTC()
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	proj, _ := projects.Create(ctx, user.ID, "", "alpha", now)
	s, _ := stories.Create(ctx, story.Story{
		ProjectID: proj.ID,
		Title:     "composite",
		Status:    "in_progress",
		Priority:  "high",
		Category:  "feature",
		Tags:      []string{"source:ui-design.md#story-view"},
		CreatedBy: user.ID,
	}, now)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	rec, comp := renderComposite(t, p, s.ID, sess.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", rec.Header().Get("Content-Type"))
	}
	if comp.Story.ID != s.ID {
		t.Errorf("composite story id = %q, want %q", comp.Story.ID, s.ID)
	}
	if len(comp.SourceDocs) != 1 || comp.SourceDocs[0].Path != "ui-design.md" {
		t.Errorf("composite source docs = %+v, want one ui-design.md row", comp.SourceDocs)
	}
}

func TestStoryView_CrossOwner404(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, _, stories := newTestPortal(t, &config.Config{Env: "dev"})
	ctx := context.Background()
	now := time.Now().UTC()
	alice := auth.User{ID: "u_alice", Email: "alice@local"}
	bob := auth.User{ID: "u_bob", Email: "bob@local"}
	users.Add(alice)
	users.Add(bob)
	proj, _ := projects.Create(ctx, alice.ID, "", "alice-only", now)
	s, _ := stories.Create(ctx, story.Story{
		ProjectID: proj.ID,
		Title:     "alice-only-story",
		Status:    "in_progress",
		Priority:  "high",
		Category:  "feature",
		CreatedBy: alice.ID,
	}, now)
	sess, _ := sessions.Create(bob.ID, auth.DefaultSessionTTL)

	rec := renderStoryDetail(t, p, proj.ID, s.ID, sess.ID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (no leak)", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "alice-only-story") {
		t.Errorf("404 body must not leak story title")
	}

	// Composite endpoint mirrors the 404.
	rec, _ = renderComposite(t, p, s.ID, sess.ID)
	if rec.Code != http.StatusNotFound {
		t.Errorf("composite status = %d, want 404", rec.Code)
	}
}

func TestSourceDocsForStory_ParsesAnchors(t *testing.T) {
	t.Parallel()
	s := story.Story{Tags: []string{
		"epic:portal-views",
		"source:ui-design.md#story-view",
		"source:architecture.md",
		"source:",     // ignored: empty body
		"depends:foo", // ignored: not source
	}}
	got := sourceDocsForStory(s)
	if len(got) != 2 {
		t.Fatalf("parsed %d, want 2: %+v", len(got), got)
	}
	if got[0].Path != "ui-design.md" || got[0].Anchor != "story-view" {
		t.Errorf("first = %+v, want path=ui-design.md anchor=story-view", got[0])
	}
	if got[1].Path != "architecture.md" || got[1].Anchor != "" {
		t.Errorf("second = %+v, want path=architecture.md anchor=\"\"", got[1])
	}
}

func TestApplyDeliveryVerdict_Resolution(t *testing.T) {
	t.Parallel()
	cases := []struct {
		verdict string
		want    string
	}{
		{"approved", "delivered"},
		{"rejected", "failed"},
		{"needs_changes", "partially_delivered"},
		{"amended", "partially_delivered"},
		{"unknown", ""},
	}
	for _, c := range cases {
		strip := applyDeliveryVerdict(deliveryStrip{Status: "done"}, []verdictCard{{
			ContractName: "story_close",
			Verdict:      c.verdict,
		}})
		if strip.Resolution != c.want {
			t.Errorf("verdict=%q resolution=%q, want %q", c.verdict, strip.Resolution, c.want)
		}
	}
}
