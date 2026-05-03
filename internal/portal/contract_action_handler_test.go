package portal

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/story"
)

// postContractAction drives a POST /api/contracts/{id}/{verb} request.
// Verb is "close" (complete) or "review-close" (review).
// sty_82662a66.
func postContractAction(t *testing.T, p *Portal, ciID, verb, sessionCookie string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	p.Register(mux)
	body := bytes.NewReader([]byte(`{}`))
	req := httptest.NewRequest(http.MethodPost, "/api/contracts/"+ciID+"/"+verb, body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sessionCookie})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestContractComplete_FromClaimed_HappyPath asserts: status=claimed →
// 200, CI flips to passed, kind:operator-override row written.
func TestContractComplete_FromClaimed_HappyPath(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, ledgerStore, stories, contracts, docs, _ := newTestPortalWithContracts(t, &config.Config{Env: "dev"})
	ctx := context.Background()
	now := time.Now().UTC()
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	proj, _ := projects.Create(ctx, user.ID, "wksp_a", "alpha", now)
	st, _ := stories.Create(ctx, story.Story{
		WorkspaceID: proj.WorkspaceID, ProjectID: proj.ID,
		Title: "ops-complete", Status: story.StatusInProgress, CreatedBy: user.ID,
	}, now)
	contractDoc, _ := docs.Create(ctx, document.Document{Name: "plan", Type: "contract", Scope: "system", Status: "active", Body: "x"}, now)
	ci, _ := contracts.Create(ctx, contract.ContractInstance{
		StoryID:      st.ID,
		ContractID:   contractDoc.ID,
		ContractName: "plan",
		Sequence:     0,
		Status:       contract.StatusClaimed,
	}, now)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	rec := postContractAction(t, p, ci.ID, "close", sess.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["status"] != contract.StatusPassed {
		t.Errorf("ci status = %v, want %s", resp["status"], contract.StatusPassed)
	}
	rows, _ := ledgerStore.List(ctx, proj.ID, ledger.ListOptions{StoryID: st.ID, Tags: []string{"kind:operator-override"}}, nil)
	if len(rows) != 1 {
		t.Fatalf("expected 1 operator-override ledger row; got %d", len(rows))
	}
	if rows[0].ContractID == nil || *rows[0].ContractID != ci.ID {
		t.Errorf("audit row contract_id mismatch")
	}
}

// TestContractComplete_FromReady_ChainsThroughClaimed asserts the
// ready→claimed→passed chain. The single POST should leave the CI in
// passed and emit one operator-override audit row.
func TestContractComplete_FromReady_ChainsThroughClaimed(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, _, stories, contracts, docs, _ := newTestPortalWithContracts(t, &config.Config{Env: "dev"})
	ctx := context.Background()
	now := time.Now().UTC()
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	proj, _ := projects.Create(ctx, user.ID, "wksp_a", "alpha", now)
	st, _ := stories.Create(ctx, story.Story{
		WorkspaceID: proj.WorkspaceID, ProjectID: proj.ID,
		Title: "ops-chain", Status: story.StatusReady, CreatedBy: user.ID,
	}, now)
	contractDoc, _ := docs.Create(ctx, document.Document{Name: "plan", Type: "contract", Scope: "system", Status: "active", Body: "x"}, now)
	ci, _ := contracts.Create(ctx, contract.ContractInstance{
		StoryID: st.ID, ContractID: contractDoc.ID, ContractName: "plan", Sequence: 0, Status: contract.StatusReady,
	}, now)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	rec := postContractAction(t, p, ci.ID, "close", sess.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	got, _ := contracts.GetByID(ctx, ci.ID, nil)
	if got.Status != contract.StatusPassed {
		t.Errorf("ci status = %s, want %s", got.Status, contract.StatusPassed)
	}
}

// TestContractComplete_TerminalCI_Rejected asserts: passed/failed/
// skipped → 422.
func TestContractComplete_TerminalCI_Rejected(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, _, stories, contracts, docs, _ := newTestPortalWithContracts(t, &config.Config{Env: "dev"})
	ctx := context.Background()
	now := time.Now().UTC()
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	proj, _ := projects.Create(ctx, user.ID, "wksp_a", "alpha", now)
	st, _ := stories.Create(ctx, story.Story{
		WorkspaceID: proj.WorkspaceID, ProjectID: proj.ID,
		Title: "ops-terminal", Status: story.StatusInProgress, CreatedBy: user.ID,
	}, now)
	contractDoc, _ := docs.Create(ctx, document.Document{Name: "plan", Type: "contract", Scope: "system", Status: "active", Body: "x"}, now)
	ci, _ := contracts.Create(ctx, contract.ContractInstance{
		StoryID: st.ID, ContractID: contractDoc.ID, ContractName: "plan", Sequence: 0, Status: contract.StatusReady,
	}, now)
	// Walk to passed via the legitimate ready→claimed→passed sequence
	// using the store directly (not the operator-override surface).
	if _, err := contracts.UpdateStatus(ctx, ci.ID, contract.StatusClaimed, user.ID, now, nil); err != nil {
		t.Fatalf("seed claimed: %v", err)
	}
	if _, err := contracts.UpdateStatus(ctx, ci.ID, contract.StatusPassed, user.ID, now, nil); err != nil {
		t.Fatalf("seed passed: %v", err)
	}
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	rec := postContractAction(t, p, ci.ID, "close", sess.ID)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
}

// TestContractReview_FromPendingReview_HappyPath asserts: status=
// pending_review + review verb → 200 + CI passed.
func TestContractReview_FromPendingReview_HappyPath(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, _, stories, contracts, docs, _ := newTestPortalWithContracts(t, &config.Config{Env: "dev"})
	ctx := context.Background()
	now := time.Now().UTC()
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	proj, _ := projects.Create(ctx, user.ID, "wksp_a", "alpha", now)
	st, _ := stories.Create(ctx, story.Story{
		WorkspaceID: proj.WorkspaceID, ProjectID: proj.ID,
		Title: "ops-review", Status: story.StatusInProgress, CreatedBy: user.ID,
	}, now)
	contractDoc, _ := docs.Create(ctx, document.Document{Name: "plan", Type: "contract", Scope: "system", Status: "active", Body: "x"}, now)
	ci, _ := contracts.Create(ctx, contract.ContractInstance{
		StoryID: st.ID, ContractID: contractDoc.ID, ContractName: "plan", Sequence: 0, Status: contract.StatusReady,
	}, now)
	if _, err := contracts.UpdateStatus(ctx, ci.ID, contract.StatusClaimed, user.ID, now, nil); err != nil {
		t.Fatalf("seed claimed: %v", err)
	}
	if _, err := contracts.UpdateStatus(ctx, ci.ID, contract.StatusPendingReview, user.ID, now, nil); err != nil {
		t.Fatalf("seed pending_review: %v", err)
	}
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	rec := postContractAction(t, p, ci.ID, "review-close", sess.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	got, _ := contracts.GetByID(ctx, ci.ID, nil)
	if got.Status != contract.StatusPassed {
		t.Errorf("ci status = %s, want %s", got.Status, contract.StatusPassed)
	}
}

// TestContractReview_AgainstReady_Rejected asserts the review verb is
// rejected when called against a CI that has not been claimed.
func TestContractReview_AgainstReady_Rejected(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, _, stories, contracts, docs, _ := newTestPortalWithContracts(t, &config.Config{Env: "dev"})
	ctx := context.Background()
	now := time.Now().UTC()
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	proj, _ := projects.Create(ctx, user.ID, "wksp_a", "alpha", now)
	st, _ := stories.Create(ctx, story.Story{
		WorkspaceID: proj.WorkspaceID, ProjectID: proj.ID,
		Title: "ops-review-fail", Status: story.StatusInProgress, CreatedBy: user.ID,
	}, now)
	contractDoc, _ := docs.Create(ctx, document.Document{Name: "plan", Type: "contract", Scope: "system", Status: "active", Body: "x"}, now)
	ci, _ := contracts.Create(ctx, contract.ContractInstance{
		StoryID: st.ID, ContractID: contractDoc.ID, ContractName: "plan", Sequence: 0, Status: contract.StatusReady,
	}, now)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	rec := postContractAction(t, p, ci.ID, "review-close", sess.ID)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
}

// TestContractAction_CrossOwner_404 asserts: caller is not the project
// owner → 404 (mirrors handleProjectDetail's leak-prevention pattern).
func TestContractAction_CrossOwner_404(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, _, stories, contracts, docs, _ := newTestPortalWithContracts(t, &config.Config{Env: "dev"})
	ctx := context.Background()
	now := time.Now().UTC()
	owner := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(owner)
	stranger := auth.User{ID: "u_eve", Email: "eve@local"}
	users.Add(stranger)
	proj, _ := projects.Create(ctx, owner.ID, "wksp_a", "alpha", now)
	st, _ := stories.Create(ctx, story.Story{
		WorkspaceID: proj.WorkspaceID, ProjectID: proj.ID,
		Title: "ops-crossowner", Status: story.StatusInProgress, CreatedBy: owner.ID,
	}, now)
	contractDoc, _ := docs.Create(ctx, document.Document{Name: "plan", Type: "contract", Scope: "system", Status: "active", Body: "x"}, now)
	ci, _ := contracts.Create(ctx, contract.ContractInstance{
		StoryID: st.ID, ContractID: contractDoc.ID, ContractName: "plan", Sequence: 0, Status: contract.StatusClaimed,
	}, now)
	sess, _ := sessions.Create(stranger.ID, auth.DefaultSessionTTL)

	rec := postContractAction(t, p, ci.ID, "close", sess.ID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// TestContractAction_UnknownID_404 asserts: caller hits a CI id that
// doesn't exist → 404.
func TestContractAction_UnknownID_404(t *testing.T) {
	t.Parallel()
	p, users, sessions, _, _, _, _, _, _ := newTestPortalWithContracts(t, &config.Config{Env: "dev"})
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	rec := postContractAction(t, p, "ci_doesnotexist", "close", sess.ID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// TestContractAction_Unauth_401 asserts an empty cookie → 401.
func TestContractAction_Unauth_401(t *testing.T) {
	t.Parallel()
	p, _, _, _, _, _, _, _, _ := newTestPortalWithContracts(t, &config.Config{Env: "dev"})
	rec := postContractAction(t, p, "ci_anything", "close", "no-such-cookie")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}
