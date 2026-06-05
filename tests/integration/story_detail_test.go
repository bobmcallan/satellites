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
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/server"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

const testWorkflowSkill = "---\n" +
	"name: test-fix-workflow\n" +
	"kind: workflow\n" +
	"applies_to: [fix]\n" +
	"description: test workflow\n" +
	"---\n" +
	"# test workflow\n\n" +
	"```yaml\n" +
	"states: [backlog, in_progress, done]\n" +
	"transitions:\n" +
	"  - {from: backlog, to: in_progress, reviewer_skill: \"satellites-story-plan-review\"}\n" +
	"  - {from: in_progress, to: done, reviewer_skill: \"satellites-story-done-review\"}\n" +
	"```\n"

// TestStoryDetailQAView exercises sty_0915e1b7: the per-story QA process view
// (declared workflow × actual ledger) and the SSE realtime stream, end-to-end
// over the real server handler + Postgres.
func TestStoryDetailQAView(t *testing.T) {
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
	t0 := time.Now().UTC()
	ws, err := wsStore.Create(ctx, "", "qa-ws", t0)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "qa-project"}, t0)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	// A fix story currently in_progress.
	fixCat := "fix"
	createReq, _ := json.Marshal(verb.DocumentUpsertRequest{
		Type: "story", ProjectID: pj.ID, Name: "trace story", Category: &fixCat,
	})
	createRaw, err := verb.Dispatch(ctx, "document_upsert", createReq)
	if err != nil {
		t.Fatalf("create story: %v", err)
	}
	var created verb.DocumentUpsertResponse
	if err := json.Unmarshal(createRaw, &created); err != nil {
		t.Fatalf("decode story: %v", err)
	}
	storyID := created.Document.ID

	// Move it to in_progress so the done edge reads pending.
	statusInProgress := "in_progress"
	patchReq, _ := json.Marshal(verb.DocumentUpsertRequest{ID: storyID, Status: &statusInProgress})
	if _, err := verb.Dispatch(ctx, "document_upsert", patchReq); err != nil {
		t.Fatalf("patch status: %v", err)
	}

	// Seed the declared process (workflow skill) directly into the store —
	// document_upsert of a type:skill row is membership-gated; the store call
	// bypasses the verb auth the way the test seeds other substrate.
	if _, _, err := docStore.Upsert(ctx, document.UpsertInput{
		Key:  document.Key{Scope: document.ScopeProject, WorkspaceID: ws.ID, ProjectID: pj.ID, Name: "test-fix-workflow"},
		Type: document.TypeSkill,
		Body: testWorkflowSkill,
	}, t0); err != nil {
		t.Fatalf("upsert workflow skill: %v", err)
	}

	// Seed the actual process: plan-review accepted backlog→in_progress.
	// Reviewer-enacted kinds are role-gated through the verb, so seed via the
	// store directly.
	appendLedger := func(kind, body string, payload map[string]any, at time.Time) {
		t.Helper()
		pb, _ := json.Marshal(payload)
		if _, err := ledStore.Append(ctx, ledger.AppendInput{
			StoryID: storyID, ProjectID: pj.ID, WorkspaceID: ws.ID,
			Kind: kind, Body: body, Payload: pb, Actor: "usr_dev_admin",
		}, at); err != nil {
			t.Fatalf("append %s: %v", kind, err)
		}
	}
	appendLedger("review_accept", "plan ready", map[string]any{"gate": "satellites-story-plan-review", "from_status": "backlog", "to_status": "in_progress"}, t0)
	appendLedger("status_transition", "backlog → in_progress", map[string]any{"from_status": "backlog", "to_status": "in_progress"}, t0.Add(time.Second))

	sessions := auth.NewSessions([]byte("qa-test-secret"))
	handler := server.Build(server.Config{Store: authStore, Sessions: sessions, DevMode: true})

	sessionCookies := func() []*http.Cookie {
		rec := httptest.NewRecorder()
		sessions.Issue(rec, "usr_dev_admin")
		return rec.Result().Cookies()
	}

	t.Run("static view reconciles declared workflow against the ledger", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/stories/"+storyID, nil)
		for _, c := range sessionCookies() {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		body, _ := io.ReadAll(rec.Result().Body)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d, body=%s", rec.Code, body)
		}
		s := string(body)
		for _, want := range []string{
			"test-fix-workflow", // declared workflow name
			"backlog", "in_progress", "done",
			`data-status="accepted"`, // plan-review fired
			`data-status="pending"`,  // done edge is the next move
			"plan ready",             // accept verdict surfaced
			`data-table="process-trace"`,
		} {
			if !strings.Contains(s, want) {
				t.Errorf("view missing %q", want)
			}
		}
	})

	// sty_96cc0ade retired the per-story SSE (/api/stories/{id}/events); the QA
	// view now refetches the trace fragment off the shared bus. Assert the old
	// endpoint no longer serves a stream and the fragment renders the trace.
	t.Run("retired per-story SSE is gone; trace fragment serves the reconciled view", func(t *testing.T) {
		srv := httptest.NewServer(handler)
		defer srv.Close()

		oldReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/stories/"+storyID+"/events", nil)
		for _, c := range sessionCookies() {
			oldReq.AddCookie(c)
		}
		oldResp, err := http.DefaultClient.Do(oldReq)
		if err != nil {
			t.Fatalf("old endpoint request: %v", err)
		}
		defer oldResp.Body.Close()
		if oldResp.StatusCode == http.StatusOK &&
			strings.Contains(oldResp.Header.Get("Content-Type"), "text/event-stream") {
			t.Fatalf("retired /api/stories/{id}/events still serves an SSE stream (status %d)", oldResp.StatusCode)
		}

		fragReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/stories/"+storyID+"/trace.fragment", nil)
		for _, c := range sessionCookies() {
			fragReq.AddCookie(c)
		}
		fragResp, err := http.DefaultClient.Do(fragReq)
		if err != nil {
			t.Fatalf("trace fragment request: %v", err)
		}
		defer fragResp.Body.Close()
		if fragResp.StatusCode != http.StatusOK {
			t.Fatalf("trace.fragment status %d", fragResp.StatusCode)
		}
		fb, _ := io.ReadAll(fragResp.Body)
		fs := string(fb)
		for _, want := range []string{`data-table="process-trace"`, `data-field="current-status"`, "in_progress"} {
			if !strings.Contains(fs, want) {
				t.Errorf("trace fragment missing %q", want)
			}
		}
	})
}
