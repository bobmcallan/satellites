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
	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/story"
	"github.com/bobmcallan/satellites/internal/task"
)

// renderProjectTasks drives a GET /projects/{id}/tasks request.
func renderProjectTasks(t *testing.T, p *Portal, projID, sessionCookie, query string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	p.Register(mux)
	url := "/projects/" + projID + "/tasks"
	if query != "" {
		url += "?q=" + query
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sessionCookie})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestProjectTasks_RendersThreePanesWithRoleAndIteration(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, _, stories, contracts, docs, _ := newTestPortalWithContracts(t, &config.Config{Env: "dev"})
	ctx := context.Background()
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	proj, _ := projects.Create(ctx, user.ID, "wksp_a", "alpha", now)

	developDoc, _ := docs.Create(ctx, document.Document{
		Type:       document.TypeContract,
		Scope:      document.ScopeSystem,
		Name:       "develop",
		Status:     document.StatusActive,
		Structured: []byte(`{"required_role":"developer","category":"build"}`),
	}, now)
	st, _ := stories.Create(ctx, story.Story{
		ProjectID: proj.ID,
		Title:     "loop story",
		Status:    "in_progress",
		Priority:  "high",
		Category:  "feature",
		CreatedBy: user.ID,
	}, now)
	ci, _ := contracts.Create(ctx, contract.ContractInstance{
		StoryID: st.ID, ContractID: developDoc.ID, ContractName: "develop",
		Sequence: 1, Status: contract.StatusReady,
	}, now)

	taskStore := p.tasks.(*task.MemoryStore)

	// One enqueued, one claimed, one closed.
	enqPayload, _ := json.Marshal(map[string]string{"story_id": st.ID})
	enq, err := taskStore.Enqueue(ctx, task.Task{
		WorkspaceID:        proj.WorkspaceID,
		ProjectID:          proj.ID,
		ContractInstanceID: ci.ID,
		Kind:               task.KindWork,
		Iteration:          2,
		Origin:             task.OriginStoryStage,
		Priority:           task.PriorityHigh,
		Payload:            enqPayload,
	}, now.Add(-30*time.Minute))
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	clmSeed, _ := taskStore.Enqueue(ctx, task.Task{
		WorkspaceID:        proj.WorkspaceID,
		ProjectID:          proj.ID,
		ContractInstanceID: ci.ID,
		Kind:               task.KindWork,
		Iteration:          1,
		Origin:             task.OriginStoryStage,
		Priority:           task.PriorityMedium,
		Payload:            enqPayload,
	}, now.Add(-2*time.Hour))
	clm, _ := taskStore.ClaimByID(ctx, clmSeed.ID, "worker_a", now.Add(-90*time.Minute), nil)
	closeSeed, _ := taskStore.Enqueue(ctx, task.Task{
		WorkspaceID:        proj.WorkspaceID,
		ProjectID:          proj.ID,
		ContractInstanceID: ci.ID,
		Kind:               task.KindWork,
		Iteration:          1,
		Origin:             task.OriginStoryStage,
		Priority:           task.PriorityLow,
		Payload:            enqPayload,
	}, now.Add(-3*time.Hour))
	if _, err := taskStore.ClaimByID(ctx, closeSeed.ID, "worker_a", now.Add(-150*time.Minute), nil); err != nil {
		t.Fatalf("claim close-seed: %v", err)
	}
	if _, err := taskStore.Close(ctx, closeSeed.ID, task.OutcomeSuccess, now.Add(-2*time.Hour), nil); err != nil {
		t.Fatalf("close: %v", err)
	}
	_ = enq
	_ = clm

	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)
	rec := renderProjectTasks(t, p, proj.ID, sess.ID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`data-testid="project-tasks-page"`,
		`data-testid="project-tasks-filter-input"`,
		`data-testid="pane-enqueued"`,
		`data-testid="pane-in-flight"`,
		`data-testid="pane-closed"`,
		`>id<`,
		`>role<`,
		`>iter<`,
		`>story<`,
		`developer`,
		`develop`,
		`loop story`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestProjectTasks_FilterByRole(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, _, stories, _, _, _ := newTestPortalWithContracts(t, &config.Config{Env: "dev"})
	ctx := context.Background()
	now := time.Now().UTC()
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	proj, _ := projects.Create(ctx, user.ID, "wksp_a", "alpha", now)
	st, _ := stories.Create(ctx, story.Story{ProjectID: proj.ID, Title: "story", Status: "in_progress", CreatedBy: user.ID}, now)
	taskStore := p.tasks.(*task.MemoryStore)
	payload, _ := json.Marshal(map[string]string{"story_id": st.ID})
	if _, err := taskStore.Enqueue(ctx, task.Task{
		WorkspaceID: proj.WorkspaceID, ProjectID: proj.ID,
		Kind: task.KindWork, Origin: task.OriginStoryStage, Priority: task.PriorityMedium,
		Payload: payload,
	}, now); err != nil {
		t.Fatalf("enq dev: %v", err)
	}
	if _, err := taskStore.Enqueue(ctx, task.Task{
		WorkspaceID: proj.WorkspaceID, ProjectID: proj.ID,
		Kind: task.KindReview, Origin: task.OriginStoryStage, Priority: task.PriorityMedium,
		Payload: payload,
	}, now); err != nil {
		t.Fatalf("enq rev: %v", err)
	}
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)
	rec := renderProjectTasks(t, p, proj.ID, sess.ID, "role:reviewer")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "reviewer") {
		t.Errorf("filtered body missing reviewer rows")
	}
	// developer rows should be filtered out — search for the role chip
	// content specifically (not `developer` substring elsewhere).
	if strings.Count(body, ">developer<") > 0 {
		t.Errorf("filter role:reviewer should hide developer rows; body=%s", body)
	}
}
