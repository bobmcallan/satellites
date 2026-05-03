package portal

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/story"
)

// postStoryStatus drives a POST /api/stories/{id}/status request.
func postStoryStatus(t *testing.T, p *Portal, storyID, sessionCookie string, body any) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	p.Register(mux)
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/stories/"+storyID+"/status", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sessionCookie})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestStoryStatusUpdate_HappyPath_DerivableTransition asserts AC 3 +
// AC 8: a canonical-step transition (backlog→ready) succeeds with an
// empty reason and writes NO kind:operator-override row (only the
// substrate's canonical kind:story.status_change row).
func TestStoryStatusUpdate_HappyPath_DerivableTransition(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, ledgerStore, stories := newTestPortal(t, &config.Config{Env: "dev"})
	ctx := context.Background()
	now := time.Now().UTC()
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	proj, _ := projects.Create(ctx, user.ID, "wksp_a", "alpha", now)
	st, _ := stories.Create(ctx, story.Story{
		WorkspaceID: proj.WorkspaceID, ProjectID: proj.ID,
		Title: "ready-flip", Status: story.StatusBacklog, CreatedBy: user.ID,
	}, now)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	rec := postStoryStatus(t, p, st.ID, sess.ID, map[string]any{"status": "ready"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["status"] != "ready" {
		t.Errorf("response status: %v", resp["status"])
	}
	rows, _ := ledgerStore.List(ctx, proj.ID, ledger.ListOptions{StoryID: st.ID, Tags: []string{"kind:operator-override"}}, nil)
	if len(rows) != 0 {
		t.Errorf("derivable transition should NOT write kind:operator-override; got %d rows", len(rows))
	}
}

// TestStoryStatusUpdate_NonDerivableRequiresReason asserts AC 2:
// forward jump bypassing intermediate states with empty reason → 400.
func TestStoryStatusUpdate_NonDerivableRequiresReason(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, _, stories := newTestPortal(t, &config.Config{Env: "dev"})
	ctx := context.Background()
	now := time.Now().UTC()
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	proj, _ := projects.Create(ctx, user.ID, "wksp_a", "alpha", now)
	st, _ := stories.Create(ctx, story.Story{
		WorkspaceID: proj.WorkspaceID, ProjectID: proj.ID,
		Title: "skip-jump", Status: story.StatusBacklog, CreatedBy: user.ID,
	}, now)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	// Backlog → done would skip ready + in_progress. Empty reason.
	rec := postStoryStatus(t, p, st.ID, sess.ID, map[string]any{"status": "done", "reason": ""})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "reason required") {
		t.Errorf("body should mention reason requirement; got %s", rec.Body.String())
	}
}

// TestStoryStatusUpdate_NonDerivableWithReasonWritesOverrideRow asserts
// AC 7: a non-derivable transition with non-empty reason writes a
// kind:operator-override ledger row whose Content matches the reason.
func TestStoryStatusUpdate_NonDerivableWithReasonWritesOverrideRow(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, ledgerStore, stories := newTestPortal(t, &config.Config{Env: "dev"})
	ctx := context.Background()
	now := time.Now().UTC()
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	proj, _ := projects.Create(ctx, user.ID, "wksp_a", "alpha", now)
	st, _ := stories.Create(ctx, story.Story{
		WorkspaceID: proj.WorkspaceID, ProjectID: proj.ID,
		Title: "cancelled-jump", Status: story.StatusBacklog, CreatedBy: user.ID,
	}, now)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	const reason = "scope deferred to next quarter"
	rec := postStoryStatus(t, p, st.ID, sess.ID, map[string]any{"status": "cancelled", "reason": reason})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	rows, err := ledgerStore.List(ctx, proj.ID, ledger.ListOptions{StoryID: st.ID, Tags: []string{"kind:operator-override"}}, nil)
	if err != nil {
		t.Fatalf("ledger list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected exactly one kind:operator-override row; got %d", len(rows))
	}
	if rows[0].Content != reason {
		t.Errorf("override Content = %q, want %q", rows[0].Content, reason)
	}
}

// TestStoryStatusUpdate_IllegalTransitionReturns422 asserts AC: a
// transition the substrate rejects (e.g. done → backlog) returns 422.
func TestStoryStatusUpdate_IllegalTransitionReturns422(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, _, stories := newTestPortal(t, &config.Config{Env: "dev"})
	ctx := context.Background()
	now := time.Now().UTC()
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	proj, _ := projects.Create(ctx, user.ID, "wksp_a", "alpha", now)
	st, _ := stories.Create(ctx, story.Story{
		WorkspaceID: proj.WorkspaceID, ProjectID: proj.ID,
		Title: "terminal", Status: story.StatusBacklog, CreatedBy: user.ID,
	}, now)
	// Walk the story to done via the canonical chain so the substrate
	// transition matrix has a real terminal row.
	_, _ = stories.UpdateStatus(ctx, st.ID, story.StatusReady, user.ID, now.Add(time.Second), nil)
	_, _ = stories.UpdateStatus(ctx, st.ID, story.StatusInProgress, user.ID, now.Add(2*time.Second), nil)
	_, _ = stories.UpdateStatus(ctx, st.ID, story.StatusDone, user.ID, now.Add(3*time.Second), nil)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	rec := postStoryStatus(t, p, st.ID, sess.ID, map[string]any{"status": "backlog", "reason": "back to drawing board"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
}

// TestStoryStatusUpdate_CrossOwnerReturns404 asserts AC 6: posting
// against another user's story leaks neither status nor existence.
func TestStoryStatusUpdate_CrossOwnerReturns404(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, _, stories := newTestPortal(t, &config.Config{Env: "dev"})
	ctx := context.Background()
	now := time.Now().UTC()
	alice := auth.User{ID: "u_alice", Email: "alice@local"}
	bob := auth.User{ID: "u_bob", Email: "bob@local"}
	users.Add(alice)
	users.Add(bob)
	aliceProj, _ := projects.Create(ctx, alice.ID, "wksp_a", "alpha", now)
	st, _ := stories.Create(ctx, story.Story{
		WorkspaceID: aliceProj.WorkspaceID, ProjectID: aliceProj.ID,
		Title: "alice-only", Status: story.StatusBacklog, CreatedBy: alice.ID,
	}, now)
	bobSess, _ := sessions.Create(bob.ID, auth.DefaultSessionTTL)

	rec := postStoryStatus(t, p, st.ID, bobSess.ID, map[string]any{"status": "ready"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-owner POST should return 404, got %d", rec.Code)
	}
}

// TestStoryStatusUpdate_UnauthReturns401 asserts unauthenticated POST
// is rejected before any substrate write.
func TestStoryStatusUpdate_UnauthReturns401(t *testing.T) {
	t.Parallel()
	p, _, _, _, _, _ := newTestPortal(t, &config.Config{Env: "dev"})
	mux := http.NewServeMux()
	p.Register(mux)
	body, _ := json.Marshal(map[string]any{"status": "ready"})
	req := httptest.NewRequest(http.MethodPost, "/api/stories/sty_x/status", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth POST should be 401, got %d", rec.Code)
	}
}

// TestStoryStatusPanel_RenderTimeAffordances asserts AC 1 + 4 + 9: the
// stories panel renders the per-row status button-group, the row
// checkbox column, and the bulk action bar template (hidden via
// x-show until selectedIDs.size > 0).
func TestStoryStatusPanel_RenderTimeAffordances(t *testing.T) {
	t.Parallel()
	rec := renderWorkspace(t, "status:all", func(ctx context.Context, projectID string, stories *story.MemoryStore, _ *document.MemoryStore) {
		seedStory(t, stories, projectID, "render-target", "x", time.Now().UTC())
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		// Bulk action bar template.
		`data-testid="story-bulk-bar"`,
		`x-show="selectedIDs.size > 0"`,
		`data-testid="story-bulk-target"`,
		`data-testid="story-bulk-reason"`,
		`data-testid="story-bulk-apply"`,
		`data-testid="story-bulk-clear"`,
		`data-testid="story-bulk-result"`,
		// Per-row checkbox column.
		`<th class="col-select"`,
		`data-testid="story-row-select-sty_`,
		`@change="toggleRowSelection(`,
		// Per-row status button-group inside the expand.
		`data-testid="story-status-buttons-sty_`,
		`data-testid="story-status-button-sty_`,
		`-backlog`,
		`-ready`,
		`-in_progress`,
		`-done`,
		`-cancelled`,
		`data-testid="story-status-reason-sty_`,
		// The current status (backlog by default for newly-seeded
		// rows) carries `disabled` so the operator can't no-op.
		`disabled aria-pressed="true"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

// TestStoryStatusUpdate_MissingStatusReturns400 asserts the body
// validation gate.
func TestStoryStatusUpdate_MissingStatusReturns400(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, _, stories := newTestPortal(t, &config.Config{Env: "dev"})
	ctx := context.Background()
	now := time.Now().UTC()
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	proj, _ := projects.Create(ctx, user.ID, "wksp_a", "alpha", now)
	st, _ := stories.Create(ctx, story.Story{
		WorkspaceID: proj.WorkspaceID, ProjectID: proj.ID,
		Title: "missing", Status: story.StatusBacklog, CreatedBy: user.ID,
	}, now)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	rec := postStoryStatus(t, p, st.ID, sess.ID, map[string]any{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
