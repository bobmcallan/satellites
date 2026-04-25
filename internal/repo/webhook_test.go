package repo

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/task"
)

const testSecret = "shh-secret"

func newWebhookFixture(t *testing.T) (*WebhookHandler, Repo, *http.ServeMux, WebhookDeps) {
	t.Helper()
	repos := NewMemoryStore()
	tasks := task.NewMemoryStore()
	led := ledger.NewMemoryStore()

	r, err := repos.Create(context.Background(), Repo{
		WorkspaceID:   "ws_1",
		ProjectID:     "proj_a",
		GitRemote:     "git@github.com:example/r.git",
		DefaultBranch: "main",
		WebhookSecret: testSecret,
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("seed repo: %v", err)
	}

	deps := WebhookDeps{
		Repos:  repos,
		Tasks:  tasks,
		Ledger: led,
	}
	h := NewWebhookHandler(deps)
	mux := http.NewServeMux()
	h.Register(mux)
	return h, r, mux, deps
}

func githubPushBody(remote, ref string) []byte {
	body, _ := json.Marshal(map[string]any{
		"ref":   ref,
		"after": "new-sha",
		"repository": map[string]any{
			"clone_url":      remote,
			"ssh_url":        remote,
			"default_branch": "main",
		},
	})
	return body
}

func githubSign(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func newGitHubReq(body []byte, sig, deliveryID string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/webhooks/git/github", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if sig != "" {
		req.Header.Set(HeaderGitHubSignature, sig)
	}
	if deliveryID != "" {
		req.Header.Set(HeaderGitHubDelivery, deliveryID)
	}
	return req
}

func TestWebhook_HappyPath(t *testing.T) {
	t.Parallel()
	_, r, mux, deps := newWebhookFixture(t)
	body := githubPushBody(r.GitRemote, "refs/heads/main")
	req := newGitHubReq(body, githubSign(body, testSecret), "delivery-1")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["task_id"] == "" {
		t.Errorf("task_id empty in response: %s", rec.Body.String())
	}
	if resp["repo_id"] != r.ID {
		t.Errorf("repo_id = %v, want %s", resp["repo_id"], r.ID)
	}

	tasks, _ := deps.Tasks.List(context.Background(), task.ListOptions{}, nil)
	if len(tasks) != 1 {
		t.Errorf("tasks queued = %d, want 1", len(tasks))
	}

	rows, _ := deps.Ledger.List(context.Background(), "", ledger.ListOptions{}, nil)
	if !hasReindexTag(rows, tagWebhookDelivery) {
		t.Errorf("missing %s row", tagWebhookDelivery)
	}
}

func TestWebhook_SignatureMismatch(t *testing.T) {
	t.Parallel()
	_, r, mux, deps := newWebhookFixture(t)
	body := githubPushBody(r.GitRemote, "refs/heads/main")
	req := newGitHubReq(body, "sha256=deadbeef", "delivery-bad")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	rows, _ := deps.Ledger.List(context.Background(), "", ledger.ListOptions{}, nil)
	if !hasReindexTag(rows, tagWebhookAuthFailed) {
		t.Errorf("missing %s row", tagWebhookAuthFailed)
	}
	if hasReindexTag(rows, tagWebhookDelivery) {
		t.Errorf("unexpected %s row on signature mismatch", tagWebhookDelivery)
	}
	tasks, _ := deps.Tasks.List(context.Background(), task.ListOptions{}, nil)
	if len(tasks) != 0 {
		t.Errorf("tasks queued = %d on signature mismatch, want 0", len(tasks))
	}
}

func TestWebhook_UnknownRepo(t *testing.T) {
	t.Parallel()
	_, _, mux, deps := newWebhookFixture(t)
	body := githubPushBody("git@host:not-tracked.git", "refs/heads/main")
	req := newGitHubReq(body, githubSign(body, testSecret), "delivery-x")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	rows, _ := deps.Ledger.List(context.Background(), "", ledger.ListOptions{}, nil)
	if len(rows) != 0 {
		t.Errorf("ledger rows = %d, want 0 (no audit row for unknown repo per AC)", len(rows))
	}
}

func TestWebhook_DuplicateDelivery(t *testing.T) {
	t.Parallel()
	_, r, mux, deps := newWebhookFixture(t)
	body := githubPushBody(r.GitRemote, "refs/heads/main")
	sig := githubSign(body, testSecret)

	req1 := newGitHubReq(body, sig, "dup-1")
	rec1 := httptest.NewRecorder()
	mux.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusAccepted {
		t.Fatalf("first status = %d, want 202", rec1.Code)
	}
	tasksAfterFirst, _ := deps.Tasks.List(context.Background(), task.ListOptions{}, nil)

	req2 := newGitHubReq(body, sig, "dup-1")
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("duplicate status = %d, want 200", rec2.Code)
	}
	if !strings.Contains(rec2.Body.String(), "deduplicated") {
		t.Errorf("duplicate response missing deduplicated marker: %s", rec2.Body.String())
	}
	tasksAfterSecond, _ := deps.Tasks.List(context.Background(), task.ListOptions{}, nil)
	if len(tasksAfterFirst) != len(tasksAfterSecond) {
		t.Errorf("duplicate enqueued: before=%d after=%d", len(tasksAfterFirst), len(tasksAfterSecond))
	}
}

func TestWebhook_NonDefaultBranch(t *testing.T) {
	t.Parallel()
	_, r, mux, deps := newWebhookFixture(t)
	body := githubPushBody(r.GitRemote, "refs/heads/feature-x")
	req := newGitHubReq(body, githubSign(body, testSecret), "delivery-feat")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (ignored)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ignored") {
		t.Errorf("response missing ignored marker: %s", rec.Body.String())
	}
	tasks, _ := deps.Tasks.List(context.Background(), task.ListOptions{}, nil)
	if len(tasks) != 0 {
		t.Errorf("tasks queued = %d on non-default branch, want 0", len(tasks))
	}
}

func TestWebhook_UnknownProvider(t *testing.T) {
	t.Parallel()
	_, _, mux, _ := newWebhookFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/git/bitbucket", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for unknown provider", rec.Code)
	}
}

// io is unused in non-default-branch path, retained import for body
// helpers if a future test needs it.
var _ = io.EOF
