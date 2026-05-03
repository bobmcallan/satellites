// sty_f4b87ea3 — the project Stories panel renders each story's
// contracts inline in the expanded detail row. These tests pin the
// SSR shape (single-column layout, contracts sub-table, status pill,
// agent link) and the absence of the legacy "Open story →" affordance.
//
// The realtime bridge (common.js storyPanel._applyContractEvent) is
// covered by a static asset assertion: shipping the source file with
// the dispatch wired in is the contract that gives a passing Go-side
// test confidence the panel can patch live.
package portal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/story"
)

func TestProjectWorkspace_ContractsRenderInExpandedRow(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, _, stories, contracts, docs, _ := newTestPortalWithContracts(t, &config.Config{Env: "dev"})
	mux := http.NewServeMux()
	p.Register(mux)

	ctx := context.Background()
	now := time.Now().UTC()
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	proj, _ := projects.Create(ctx, user.ID, "", "alpha", now)
	s, _ := stories.Create(ctx, story.Story{
		ProjectID: proj.ID,
		Title:     "story with contracts",
		Status:    "in_progress",
		Priority:  "high",
		Category:  "feature",
		CreatedBy: user.ID,
	}, now)

	contractDoc, err := docs.Create(ctx, document.Document{
		Name: "plan", Type: "contract", Scope: "system", Status: "active", Body: "x",
	}, now)
	if err != nil {
		t.Fatalf("seed contract doc: %v", err)
	}
	agentDoc, err := docs.Create(ctx, document.Document{
		Name: "alice-the-agent", Type: "agent", Scope: "system", Status: "active", Body: "x",
	}, now)
	if err != nil {
		t.Fatalf("seed agent doc: %v", err)
	}

	ci, err := contracts.Create(ctx, contract.ContractInstance{
		StoryID:      s.ID,
		ContractID:   contractDoc.ID,
		ContractName: "plan",
		Sequence:     0,
		Status:       contract.StatusReady,
	}, now)
	if err != nil {
		t.Fatalf("seed CI: %v", err)
	}
	if _, err := contracts.SetAgent(ctx, ci.ID, agentDoc.ID, now, nil); err != nil {
		t.Fatalf("set agent: %v", err)
	}

	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)
	body := renderProjectDetailBody(t, mux, proj.ID, sess.ID)

	// AC2 — tasks sub-table renders with sequence, name, status, agent.
	wants := []string{
		`data-testid="story-tasks-` + s.ID + `"`,
		`data-testid="story-task-row-` + ci.ID + `"`,
		`data-ci-id="` + ci.ID + `"`,
		`>plan</code>`,
		`status-ready`,
		`data-testid="story-task-status-` + ci.ID + `"`,
		`data-testid="story-task-agent-` + ci.ID + `"`,
		`href="/documents/` + agentDoc.ID + `"`,
		`alice-the-agent`,
		// Single-column wrapper, not the legacy 2-column grid.
		`class="story-detail-flat"`,
	}
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("body missing %q", w)
		}
	}
	// AC1+AC5 — legacy grid + Open story link are both gone.
	mustNot := []string{
		`class="story-detail-grid"`,
		`Open story →`,
		`/projects/` + proj.ID + `/stories/` + s.ID + `"`,
	}
	for _, m := range mustNot {
		if strings.Contains(body, m) {
			t.Errorf("body still contains %q after sty_f4b87ea3", m)
		}
	}
}

func TestProjectWorkspace_ContractsEmptyStateForStoryWithoutContracts(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, _, stories, _, _, _ := newTestPortalWithContracts(t, &config.Config{Env: "dev"})
	mux := http.NewServeMux()
	p.Register(mux)

	ctx := context.Background()
	now := time.Now().UTC()
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	proj, _ := projects.Create(ctx, user.ID, "", "alpha", now)
	s, _ := stories.Create(ctx, story.Story{
		ProjectID: proj.ID, Title: "bare", Status: "backlog", Priority: "medium",
	}, now)

	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)
	body := renderProjectDetailBody(t, mux, proj.ID, sess.ID)

	want := `data-testid="story-tasks-empty-` + s.ID + `"`
	if !strings.Contains(body, want) {
		t.Errorf("body missing %q (empty tasks state)", want)
	}
}

// TestPortal_StoryPanelJS_HandlesContractEvents pins the realtime bridge
// so a future refactor can't quietly drop contract_instance.* dispatch.
// The test reads pages/static/common.js directly (the same pattern
// ws_indicator_test.go uses) and asserts the dispatch + DOM hooks are
// present in source.
func TestPortal_StoryPanelJS_HandlesContractEvents(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("../../pages/static/common.js")
	if err != nil {
		t.Fatalf("read common.js: %v", err)
	}
	body := string(src)
	for _, want := range []string{
		// Central dispatch.
		"_applyEvent(ev, projectID)",
		// Contract branch handler.
		"_applyContractEvent(ev, projectID)",
		"contract_instance.",
		// DOM hooks the handler reads.
		`section[data-story-tasks="`,
		`tr.story-task-row[data-ci-id="`,
		// Append-on-new-CI helper exists.
		"_appendContractRow(",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("common.js missing %q", want)
		}
	}
}

// renderProjectDetailBody is a tiny helper local to this file: signs in
// alice via her session cookie and returns the rendered project_detail
// body. Mirrors the pattern in project_workspace_view_test.go without
// re-defining renderWorkspace's seed callback shape.
func renderProjectDetailBody(t *testing.T, mux *http.ServeMux, projectID, sessionID string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/projects/"+projectID, nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sessionID})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}
