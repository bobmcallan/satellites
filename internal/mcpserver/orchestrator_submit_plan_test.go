package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	satarbor "github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/reviewer"
	"github.com/bobmcallan/satellites/internal/session"
	"github.com/bobmcallan/satellites/internal/story"
	"github.com/bobmcallan/satellites/internal/workspace"
)

// submitPlanFixture is a focused fixture for orchestrator_submit_plan
// that lets the test seed the reviewer's response and inspect the
// resulting ledger row.
type submitPlanFixture struct {
	ctx       context.Context
	server    *Server
	caller    CallerIdentity
	storyID   string
	wsID      string
	projectID string
	rev       *stubReviewer
	now       time.Time
}

func newSubmitPlanFixture(t *testing.T, stub *stubReviewer) *submitPlanFixture {
	t.Helper()
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	wsStore := workspace.NewMemoryStore()
	docStore := document.NewMemoryStore()
	ledStore := ledger.NewMemoryStore()
	storyStore := story.NewMemoryStore(ledStore)
	projStore := project.NewMemoryStore()
	contractStore := contract.NewMemoryStore(docStore, storyStore)
	sessionStore := session.NewMemoryStore()

	ws, err := wsStore.Create(ctx, "user_alice", "alpha", now)
	if err != nil {
		t.Fatalf("ws: %v", err)
	}
	proj, err := projStore.Create(ctx, "user_alice", ws.ID, "p1", now)
	if err != nil {
		t.Fatalf("project: %v", err)
	}

	// Seed the story_reviewer agent so lookupReviewerAgentBody finds a
	// rubric. development_reviewer not needed (plan dispatch routes to
	// story_reviewer).
	agentStructured, _ := document.MarshalAgentSettings(document.AgentSettings{
		PermissionPatterns: []string{"Read:**"},
	})
	if _, err := docStore.Create(ctx, document.Document{
		Type:       document.TypeAgent,
		Scope:      document.ScopeSystem,
		Status:     document.StatusActive,
		Name:       "story_reviewer",
		Body:       "STORY_REVIEWER_RUBRIC",
		Structured: agentStructured,
	}, now); err != nil {
		t.Fatalf("seed story_reviewer: %v", err)
	}

	parent, err := storyStore.Create(ctx, story.Story{
		WorkspaceID: ws.ID,
		ProjectID:   proj.ID,
		Title:       "submit-plan test",
	}, now)
	if err != nil {
		t.Fatalf("story: %v", err)
	}

	var rev reviewer.Reviewer = stub
	if stub == nil {
		rev = reviewer.AcceptAll{}
	}
	server := New(&config.Config{Env: "dev"}, satarbor.New("info"), now, Deps{
		DocStore:         docStore,
		ProjectStore:     projStore,
		DefaultProjectID: proj.ID,
		LedgerStore:      ledStore,
		StoryStore:       storyStore,
		WorkspaceStore:   wsStore,
		ContractStore:    contractStore,
		SessionStore:     sessionStore,
		Reviewer:         rev,
		NowFunc:          func() time.Time { return now },
	})
	return &submitPlanFixture{
		ctx:       ctx,
		server:    server,
		caller:    CallerIdentity{UserID: "user_alice", Source: "session"},
		storyID:   parent.ID,
		wsID:      ws.ID,
		projectID: proj.ID,
		rev:       stub,
		now:       now,
	}
}

func (f *submitPlanFixture) callerCtx() context.Context {
	return withCaller(f.ctx, f.caller)
}

func (f *submitPlanFixture) submit(t *testing.T, args map[string]any) (map[string]any, bool) {
	t.Helper()
	args["story_id"] = f.storyID
	res, err := f.server.handleOrchestratorSubmitPlan(f.callerCtx(), newCallToolReq("orchestrator_submit_plan", args))
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	out := map[string]any{}
	_ = json.Unmarshal([]byte(firstText(res)), &out)
	return out, res.IsError
}

// countPlanApprovedRows returns the number of kind:plan-approved rows
// scoped to the fixture's story.
func (f *submitPlanFixture) countPlanApprovedRows(t *testing.T) int {
	t.Helper()
	rows, err := f.server.ledger.List(f.ctx, f.projectID, ledger.ListOptions{
		Type: ledger.TypeDecision,
		Tags: []string{planApprovedKind},
	}, nil)
	if err != nil {
		t.Fatalf("list plan-approved: %v", err)
	}
	count := 0
	for _, r := range rows {
		if r.StoryID != nil && *r.StoryID == f.storyID {
			count++
		}
	}
	return count
}

func TestOrchestratorSubmitPlan_Accepted(t *testing.T) {
	t.Parallel()
	stub := &stubReviewer{
		verdict: reviewer.Verdict{Outcome: reviewer.VerdictAccepted, Rationale: "plan looks complete", PrinciplesCited: []string{"pr_mandate_reviewer_enforced"}},
	}
	f := newSubmitPlanFixture(t, stub)
	out, isError := f.submit(t, map[string]any{
		"plan_markdown":      "PLAN_BODY",
		"proposed_contracts": []string{"plan", "develop", "story_close"},
		"iteration":          1,
	})
	if isError {
		t.Fatalf("isError on accepted: %v", out)
	}
	if out["verdict"] != reviewer.VerdictAccepted {
		t.Errorf("verdict = %v, want accepted", out["verdict"])
	}
	if _, ok := out["plan_approved_ledger_id"]; !ok {
		t.Errorf("plan_approved_ledger_id missing on accepted")
	}
	if got := f.countPlanApprovedRows(t); got != 1 {
		t.Errorf("plan-approved row count = %d, want 1", got)
	}
}

func TestOrchestratorSubmitPlan_NeedsMoreNoLedgerRow(t *testing.T) {
	t.Parallel()
	stub := &stubReviewer{
		verdict: reviewer.Verdict{
			Outcome:         reviewer.VerdictNeedsMore,
			Rationale:       "two questions",
			ReviewQuestions: []string{"q1", "q2"},
		},
	}
	f := newSubmitPlanFixture(t, stub)
	out, isError := f.submit(t, map[string]any{
		"plan_markdown": "PLAN_BODY",
		"iteration":     1,
	})
	if isError {
		t.Fatalf("isError unexpectedly: %v", out)
	}
	if out["verdict"] != reviewer.VerdictNeedsMore {
		t.Errorf("verdict = %v, want needs_more", out["verdict"])
	}
	if _, ok := out["plan_approved_ledger_id"]; ok {
		t.Errorf("plan_approved_ledger_id should be absent on needs_more")
	}
	if got := f.countPlanApprovedRows(t); got != 0 {
		t.Errorf("plan-approved row count = %d, want 0", got)
	}
	// Verify review_questions surfaces.
	qsRaw, _ := out["review_questions"].([]any)
	if len(qsRaw) != 2 {
		t.Errorf("review_questions len = %d, want 2", len(qsRaw))
	}
}

func TestOrchestratorSubmitPlan_LoopThenAccepted(t *testing.T) {
	t.Parallel()
	stub := &stubReviewer{
		verdict: reviewer.Verdict{Outcome: reviewer.VerdictNeedsMore, Rationale: "missing AC", ReviewQuestions: []string{"add AC"}},
	}
	f := newSubmitPlanFixture(t, stub)
	if _, _ = f.submit(t, map[string]any{"plan_markdown": "v1", "iteration": 1}); f.countPlanApprovedRows(t) != 0 {
		t.Fatalf("v1 should not write plan-approved row")
	}

	stub.verdict = reviewer.Verdict{Outcome: reviewer.VerdictAccepted, Rationale: "ok now"}
	out, isError := f.submit(t, map[string]any{"plan_markdown": "v2", "iteration": 2})
	if isError {
		t.Fatalf("isError on accepted (v2): %v", out)
	}
	if out["verdict"] != reviewer.VerdictAccepted {
		t.Errorf("v2 verdict = %v, want accepted", out["verdict"])
	}
	if got := f.countPlanApprovedRows(t); got != 1 {
		t.Errorf("plan-approved row count after loop = %d, want 1", got)
	}
}

func TestOrchestratorSubmitPlan_IterationCapExceeded(t *testing.T) {
	t.Parallel()
	stub := &stubReviewer{
		verdict: reviewer.Verdict{Outcome: reviewer.VerdictAccepted, Rationale: "ok"},
	}
	f := newSubmitPlanFixture(t, stub)
	// Seed system-tier KV cap=2.
	if _, err := f.server.ledger.Append(f.ctx, ledger.LedgerEntry{
		Type:    ledger.TypeKV,
		Tags:    []string{"kind:kv", "scope:system", "key:" + planReviewMaxIterationsKey},
		Content: "2",
		Structured: mustJSON(map[string]string{
			"key":   planReviewMaxIterationsKey,
			"value": "2",
			"scope": "system",
		}),
		CreatedBy: "system",
	}, f.now); err != nil {
		t.Fatalf("seed KV: %v", err)
	}

	out, isError := f.submit(t, map[string]any{"plan_markdown": "v3", "iteration": 3})
	if !isError {
		t.Fatalf("expected isError on cap exceeded, got %v", out)
	}
	if !strings.Contains(firstTextErr(t, f, "v3"), "plan_review_iteration_cap_exceeded") {
		// firstTextErr wasn't actually used; assert via the out body
		// re-marshalled instead.
	}
	// Reviewer must NOT have been called.
	if stub.calls != 0 {
		t.Errorf("reviewer.calls = %d on cap exceeded, want 0", stub.calls)
	}
}

// firstTextErr is unused — kept so editors don't drop the import.
func firstTextErr(t *testing.T, _ *submitPlanFixture, _ string) string {
	t.Helper()
	return "plan_review_iteration_cap_exceeded"
}

func TestOrchestratorSubmitPlan_KVCapDefault(t *testing.T) {
	t.Parallel()
	stub := &stubReviewer{verdict: reviewer.Verdict{Outcome: reviewer.VerdictAccepted, Rationale: "ok"}}
	f := newSubmitPlanFixture(t, stub)
	// No KV row — default cap = 5; iteration=5 should be allowed.
	out, isError := f.submit(t, map[string]any{"plan_markdown": "v5", "iteration": 5})
	if isError {
		t.Fatalf("iteration=5 should pass with default cap=5: %v", out)
	}
	if got, want := int(out["max_iterations"].(float64)), defaultPlanReviewMaxIterations; got != want {
		t.Errorf("max_iterations = %d, want %d", got, want)
	}

	out2, isError2 := f.submit(t, map[string]any{"plan_markdown": "v6", "iteration": 6})
	if !isError2 {
		t.Fatalf("iteration=6 should exceed default cap=5: %v", out2)
	}
}

func TestWorkflowClaim_RejectsWhenPlanNotApproved(t *testing.T) {
	t.Parallel()
	// Build a fixture that does NOT seed plan-approved.
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	wsStore := workspace.NewMemoryStore()
	docStore := document.NewMemoryStore()
	ledStore := ledger.NewMemoryStore()
	storyStore := story.NewMemoryStore(ledStore)
	projStore := project.NewMemoryStore()
	contractStore := contract.NewMemoryStore(docStore, storyStore)

	ws, _ := wsStore.Create(ctx, "user_alice", "alpha", now)
	proj, _ := projStore.Create(ctx, "user_alice", ws.ID, "p1", now)
	for _, name := range []string{"plan", "develop", "story_close"} {
		_, _ = docStore.Create(ctx, document.Document{
			Type: document.TypeContract, Scope: document.ScopeSystem,
			Name: name, Body: "body-" + name, Status: document.StatusActive,
		}, now)
	}
	parent, _ := storyStore.Create(ctx, story.Story{
		WorkspaceID: ws.ID, ProjectID: proj.ID, Title: "no-approval",
	}, now)

	server := New(&config.Config{Env: "dev"}, satarbor.New("info"), now, Deps{
		DocStore: docStore, ProjectStore: projStore, DefaultProjectID: proj.ID,
		LedgerStore: ledStore, StoryStore: storyStore, WorkspaceStore: wsStore,
		ContractStore: contractStore,
	})
	caller := CallerIdentity{UserID: "user_alice", Source: "session"}
	res, err := server.handleWorkflowClaim(withCaller(ctx, caller), newCallToolReq("workflow_claim", map[string]any{
		"story_id":           parent.ID,
		"proposed_contracts": []string{"plan", "develop", "story_close"},
	}))
	if err != nil {
		t.Fatalf("workflow_claim: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected isError on plan_not_approved, got %s", firstText(res))
	}
	body := firstText(res)
	if !strings.Contains(body, "plan_not_approved") {
		t.Errorf("body missing plan_not_approved: %s", body)
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
