package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/task"
)

// closeFixture is newClaimFixture with a helper for claiming a CI.
type closeFixture struct {
	*claimFixture
}

func newCloseFixture(t *testing.T) *closeFixture {
	return &closeFixture{claimFixture: newClaimFixture(t)}
}

// claim advances a CI from ready → claimed via the handler so the
// subsequent close path has a realistic input. story_cc55e093: claim
// allocates the matching lifecycle agent for the CI's phase.
func (f *closeFixture) claim(t *testing.T, idx int, plan string) {
	t.Helper()
	res, err := f.server.handleContractClaim(f.callerCtx(), newCallToolReq("contract_claim", map[string]any{
		"contract_instance_id": f.cis[idx].ID,
		"session_id":           f.sessionID,
		"agent_id":             f.agentFor(idx),
		"plan_markdown":        plan,
	}))
	if err != nil || res.IsError {
		t.Fatalf("claim[%d]: err=%v text=%s", idx, err, firstText(res))
	}
}

func TestClose_HappyPath(t *testing.T) {
	t.Parallel()
	f := newCloseFixture(t)
	f.claim(t, 0, "plan body")

	res, err := f.server.handleContractClose(f.callerCtx(), newCallToolReq("contract_close", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"close_markdown":       "done",
		"evidence_markdown":    "evidence body",
	}))
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if res.IsError {
		t.Fatalf("close isError: %s", firstText(res))
	}
	var body struct {
		Status           string `json:"status"`
		CloseLedgerID    string `json:"close_ledger_id"`
		EvidenceLedgerID string `json:"evidence_ledger_id"`
		StoryStatus      string `json:"story_status"`
	}
	if err := json.Unmarshal([]byte(firstText(res)), &body); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if body.Status != contract.StatusPassed {
		t.Fatalf("status: %q", body.Status)
	}
	if body.CloseLedgerID == "" {
		t.Fatalf("missing close_ledger_id")
	}
	if body.EvidenceLedgerID == "" {
		t.Fatalf("missing evidence_ledger_id")
	}
	if body.StoryStatus != "" {
		t.Fatalf("story should still be in_progress: %q", body.StoryStatus)
	}
}

func TestClose_EvidenceAndCloseRequestRows(t *testing.T) {
	t.Parallel()
	f := newCloseFixture(t)
	f.claim(t, 0, "")
	res, _ := f.server.handleContractClose(f.callerCtx(), newCallToolReq("contract_close", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"close_markdown":       "done",
		"evidence_markdown":    "ev",
	}))
	var body struct {
		CloseLedgerID    string `json:"close_ledger_id"`
		EvidenceLedgerID string `json:"evidence_ledger_id"`
	}
	_ = json.Unmarshal([]byte(firstText(res)), &body)

	closeRow, err := f.server.ledger.GetByID(f.ctx, body.CloseLedgerID, nil)
	if err != nil {
		t.Fatalf("load close: %v", err)
	}
	if closeRow.Type != ledger.TypeCloseRequest {
		t.Fatalf("close type: %q", closeRow.Type)
	}
	hasCloseTag, hasPhase := false, false
	for _, tag := range closeRow.Tags {
		if tag == "kind:close-request" {
			hasCloseTag = true
		}
		if tag == "phase:close" {
			hasPhase = true
		}
	}
	if !hasCloseTag || !hasPhase {
		t.Fatalf("close row tags: %v", closeRow.Tags)
	}

	evRow, err := f.server.ledger.GetByID(f.ctx, body.EvidenceLedgerID, nil)
	if err != nil {
		t.Fatalf("load evidence: %v", err)
	}
	if evRow.Type != ledger.TypeEvidence {
		t.Fatalf("evidence type: %q", evRow.Type)
	}
}

func TestClose_RollsStoryToDone(t *testing.T) {
	t.Parallel()
	f := newCloseFixture(t)
	// Close CI[0..2] (required_for_close=true); CI[3] is the optional
	// story_close slot.
	for i := 0; i < 3; i++ {
		f.claim(t, i, "")
		res, _ := f.server.handleContractClose(f.callerCtx(), newCallToolReq("contract_close", map[string]any{
			"contract_instance_id": f.cis[i].ID,
			"close_markdown":       fmt.Sprintf("close %d", i),
		}))
		if res.IsError {
			t.Fatalf("close[%d]: %s", i, firstText(res))
		}
	}
	// Verify story transitioned to done.
	st, err := f.server.stories.GetByID(f.ctx, f.storyID, nil)
	if err != nil {
		t.Fatalf("load story: %v", err)
	}
	if st.Status != "done" {
		t.Fatalf("story status: %q want done", st.Status)
	}
}

// TestClose_PreplanReentryWorkflowClaim and
// TestClose_PreplanWorkflowSpecInvalid removed under
// `epic:v4-lifecycle-refactor` (sty_48fdaf8d) — preplan is no longer
// a contract. The plan agent owns process definition; workflow
// shape is approved during the plan-review loop, not on contract
// close. The contract_close verb no longer accepts proposed_workflow.

// TestClose_PlanRequiresChildTasks covers
// epic:v4-lifecycle-refactor sty_0c21a0cf — when a task store is
// wired and the CI being closed is the plan, contract_close rejects
// the close with plan_close_requires_tasks unless at least one task
// is enqueued against the CI.
func TestClose_PlanRequiresChildTasks(t *testing.T) {
	t.Parallel()
	f := newCloseFixture(t)

	// Wire a task store onto the existing server. Without this the gate
	// is no-op'd by design (early-boot tests pass through unchanged).
	taskStore := task.NewMemoryStore()
	f.server.tasks = taskStore

	// f.cis[0] is the plan CI per the fixture chain.
	f.claim(t, 0, "plan body")

	// Close without enqueuing any task — gate fires.
	res, err := f.server.handleContractClose(f.callerCtx(), newCallToolReq("contract_close", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"close_markdown":       "premature close",
	}))
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error; got %s", firstText(res))
	}
	if !strings.Contains(firstText(res), "plan_close_requires_tasks") {
		t.Fatalf("expected plan_close_requires_tasks; got %s", firstText(res))
	}

	// Enqueue a task bound to the plan CI; close now succeeds.
	if _, err := taskStore.Enqueue(f.ctx, task.Task{
		WorkspaceID:        f.wsID,
		ContractInstanceID: f.cis[0].ID,
		RequiredRole:       "developer",
		Origin:             task.OriginStoryStage,
		Priority:           task.PriorityMedium,
	}, f.now); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	res2, err := f.server.handleContractClose(f.callerCtx(), newCallToolReq("contract_close", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"close_markdown":       "plan done",
		"evidence_markdown":    "decomposed into 1 developer task",
	}))
	if err != nil || res2.IsError {
		t.Fatalf("close after enqueue: err=%v text=%s", err, firstText(res2))
	}
}

func TestClose_PlanDeferred(t *testing.T) {
	t.Parallel()
	f := newCloseFixture(t)
	// Claim without a plan (deferred).
	if _, err := f.server.handleContractClaim(f.callerCtx(), newCallToolReq("contract_claim", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"session_id":           f.sessionID,
		"agent_id":             f.agentFor(0),
	})); err != nil {
		t.Fatalf("claim: %v", err)
	}

	res, err := f.server.handleContractClose(f.callerCtx(), newCallToolReq("contract_close", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"close_markdown":       "plan deferred",
		"plan_markdown":        "the deferred plan",
	}))
	if err != nil || res.IsError {
		t.Fatalf("close: err=%v text=%s", err, firstText(res))
	}
	var body struct {
		PlanLedgerID string `json:"plan_ledger_id"`
	}
	_ = json.Unmarshal([]byte(firstText(res)), &body)
	if body.PlanLedgerID == "" {
		t.Fatalf("plan_ledger_id should be populated for deferred plan")
	}
	// CI should carry the new PlanLedgerID.
	ci, _ := f.server.contracts.GetByID(f.ctx, f.cis[0].ID, nil)
	if ci.PlanLedgerID != body.PlanLedgerID {
		t.Fatalf("CI PlanLedgerID: got %q want %q", ci.PlanLedgerID, body.PlanLedgerID)
	}
}

func TestRespond_WritesRow(t *testing.T) {
	t.Parallel()
	f := newCloseFixture(t)
	f.claim(t, 0, "")
	res, err := f.server.handleContractRespond(f.callerCtx(), newCallToolReq("contract_respond", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"response_markdown":    "here is the answer",
	}))
	if err != nil || res.IsError {
		t.Fatalf("respond: err=%v text=%s", err, firstText(res))
	}
	var body struct {
		ResponseLedgerID string `json:"response_ledger_id"`
	}
	_ = json.Unmarshal([]byte(firstText(res)), &body)
	if body.ResponseLedgerID == "" {
		t.Fatalf("missing response_ledger_id")
	}
	row, err := f.server.ledger.GetByID(f.ctx, body.ResponseLedgerID, nil)
	if err != nil {
		t.Fatalf("load response: %v", err)
	}
	if row.Type != ledger.TypeDecision {
		t.Fatalf("type: %q", row.Type)
	}
	hasTag := false
	for _, tag := range row.Tags {
		if tag == "kind:review-response" {
			hasTag = true
		}
	}
	if !hasTag {
		t.Fatalf("missing kind:review-response tag: %v", row.Tags)
	}
}

func TestResume_RebindSameSessionClaimed(t *testing.T) {
	t.Parallel()
	f := newCloseFixture(t)
	f.claim(t, 0, "")
	// Mint a fresh session + grant for the rebind target.
	newSess := "resume-newsession"
	newGrant := f.mintSessionGrant(t, newSess)
	res, err := f.server.handleContractResume(f.callerCtx(), newCallToolReq("contract_resume", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"session_id":           newSess,
		"reason":               "rebind",
	}))
	if err != nil || res.IsError {
		t.Fatalf("resume: err=%v text=%s", err, firstText(res))
	}
	ci, _ := f.server.contracts.GetByID(f.ctx, f.cis[0].ID, nil)
	if ci.ClaimedViaGrantID != newGrant {
		t.Fatalf("grant not rebound: got %q want %q", ci.ClaimedViaGrantID, newGrant)
	}
	if ci.Status != contract.StatusClaimed {
		t.Fatalf("status changed: %q", ci.Status)
	}
}

func TestResume_ReopenPassedCI(t *testing.T) {
	t.Parallel()
	f := newCloseFixture(t)
	f.claim(t, 0, "initial plan")
	// Close CI[0] → passed.
	if res, _ := f.server.handleContractClose(f.callerCtx(), newCallToolReq("contract_close", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"close_markdown":       "done",
	})); res.IsError {
		t.Fatalf("close[0]: %s", firstText(res))
	}
	// Claim + close CI[1] → passed (so downstream rollback has
	// something to flip).
	f.claim(t, 1, "")
	if res, _ := f.server.handleContractClose(f.callerCtx(), newCallToolReq("contract_close", map[string]any{
		"contract_instance_id": f.cis[1].ID,
		"close_markdown":       "done",
	})); res.IsError {
		t.Fatalf("close[1]: %s", firstText(res))
	}
	// Capture CI[0]'s plan ledger id before resume.
	before, _ := f.server.contracts.GetByID(f.ctx, f.cis[0].ID, nil)
	priorPlan := before.PlanLedgerID

	res, err := f.server.handleContractResume(f.callerCtx(), newCallToolReq("contract_resume", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"session_id":           f.sessionID,
		"reason":               "reopen to amend",
	}))
	if err != nil || res.IsError {
		t.Fatalf("resume: err=%v text=%s", err, firstText(res))
	}
	var body struct {
		Reopened      bool     `json:"reopened"`
		RolledBackCIs []string `json:"rolled_back_cis"`
	}
	_ = json.Unmarshal([]byte(firstText(res)), &body)
	if !body.Reopened {
		t.Fatalf("expected reopened=true: %s", firstText(res))
	}
	if len(body.RolledBackCIs) == 0 {
		t.Fatalf("expected downstream rollback, got none")
	}

	// CI[0] should be claimed again.
	after, _ := f.server.contracts.GetByID(f.ctx, f.cis[0].ID, nil)
	if after.Status != contract.StatusClaimed {
		t.Fatalf("CI[0] status: %q want claimed", after.Status)
	}
	// CI[1] should be back to ready.
	ci1, _ := f.server.contracts.GetByID(f.ctx, f.cis[1].ID, nil)
	if ci1.Status != contract.StatusReady {
		t.Fatalf("CI[1] not flipped back to ready: %q", ci1.Status)
	}
	// Prior plan should be dereferenced.
	if priorPlan != "" {
		planRow, err := f.server.ledger.GetByID(f.ctx, priorPlan, nil)
		if err != nil {
			t.Fatalf("load prior plan: %v", err)
		}
		if planRow.Status != ledger.StatusDereferenced {
			t.Fatalf("prior plan status: %q want dereferenced", planRow.Status)
		}
	}
}

func TestResume_CapPerCI(t *testing.T) {
	t.Parallel()
	f := newCloseFixture(t)
	f.claim(t, 0, "")

	// Drive the CI counter up to the cap by writing kv rows directly.
	for i := 1; i <= resumeCapCI; i++ {
		structured, _ := json.Marshal(map[string]any{"count": i})
		if _, err := f.server.ledger.Append(f.ctx, ledger.LedgerEntry{
			WorkspaceID: f.wsID,
			ProjectID:   f.projectID,
			StoryID:     ledger.StringPtr(f.storyID),
			Type:        ledger.TypeKV,
			Tags:        []string{"key:resume_count:ci:" + f.cis[0].ID},
			Content:     "count",
			Structured:  structured,
			CreatedBy:   "system",
		}, f.now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("seed counter %d: %v", i, err)
		}
	}

	res, err := f.server.handleContractResume(f.callerCtx(), newCallToolReq("contract_resume", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"session_id":           f.sessionID,
		"reason":               "one too many",
	}))
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if !strings.Contains(firstText(res), `"error":"resume_cap_exceeded_ci"`) {
		t.Fatalf("expected resume_cap_exceeded_ci, got %s", firstText(res))
	}
}

func TestResume_CapPerStory(t *testing.T) {
	t.Parallel()
	f := newCloseFixture(t)
	f.claim(t, 0, "")

	for i := 1; i <= resumeCapStory; i++ {
		structured, _ := json.Marshal(map[string]any{"count": i})
		if _, err := f.server.ledger.Append(f.ctx, ledger.LedgerEntry{
			WorkspaceID: f.wsID,
			ProjectID:   f.projectID,
			StoryID:     ledger.StringPtr(f.storyID),
			Type:        ledger.TypeKV,
			Tags:        []string{"key:resume_count:story:" + f.storyID},
			Content:     "count",
			Structured:  structured,
			CreatedBy:   "system",
		}, f.now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("seed counter %d: %v", i, err)
		}
	}

	res, err := f.server.handleContractResume(f.callerCtx(), newCallToolReq("contract_resume", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"session_id":           f.sessionID,
		"reason":               "story cap",
	}))
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if !strings.Contains(firstText(res), `"error":"resume_cap_exceeded_story"`) {
		t.Fatalf("expected resume_cap_exceeded_story, got %s", firstText(res))
	}
}

// Compile-time reference to avoid unused-import churn when these
// helpers exist only as convenience in tests.
var _ = context.Background

// TestClose_ModeTaskEnqueuesReviewTaskAndPendsCI covers the new path
// added by epic:v4-lifecycle-refactor sty_b6b2de01: when a contract
// document declares validation_mode=task, contract_close enqueues a
// kind:review task with required_role:reviewer and flips the CI to
// pending_review (NOT passed). The reviewer then calls
// contract_review_close to flip the CI to passed/failed.
func TestClose_ModeTaskEnqueuesReviewTaskAndPendsCI(t *testing.T) {
	t.Parallel()
	f := newCloseFixture(t)
	taskStore := task.NewMemoryStore()
	f.server.tasks = taskStore

	// Mark the plan contract document with validation_mode=task so the
	// new path fires.
	planContractID := f.cis[0].ContractID
	doc, err := f.server.docs.GetByID(f.ctx, planContractID, nil)
	if err != nil {
		t.Fatalf("get contract doc: %v", err)
	}
	taskMode, _ := json.Marshal(map[string]any{"validation_mode": "task"})
	if _, err := f.server.docs.Update(f.ctx, doc.ID, document.UpdateFields{Structured: &taskMode}, "test", f.now, nil); err != nil {
		t.Fatalf("update doc: %v", err)
	}

	// Plan-close gate from sty_0c21a0cf — enqueue a child task first.
	if _, err := taskStore.Enqueue(f.ctx, task.Task{
		WorkspaceID:        f.wsID,
		ContractInstanceID: f.cis[0].ID,
		RequiredRole:       "developer",
		Origin:             task.OriginStoryStage,
		Priority:           task.PriorityMedium,
	}, f.now); err != nil {
		t.Fatalf("enqueue child: %v", err)
	}

	f.claim(t, 0, "plan body")

	res, err := f.server.handleContractClose(f.callerCtx(), newCallToolReq("contract_close", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"close_markdown":       "plan done",
		"evidence_markdown":    "decomposed into 1 developer task",
	}))
	if err != nil || res.IsError {
		t.Fatalf("close: err=%v text=%s", err, firstText(res))
	}

	var body struct {
		Status       string `json:"status"`
		ReviewTaskID string `json:"review_task_id"`
	}
	if err := json.Unmarshal([]byte(firstText(res)), &body); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if body.Status != contract.StatusPendingReview {
		t.Fatalf("status: got %q want %q", body.Status, contract.StatusPendingReview)
	}
	if body.ReviewTaskID == "" {
		t.Fatalf("expected review_task_id")
	}
	reviewTask, err := taskStore.GetByID(f.ctx, body.ReviewTaskID, nil)
	if err != nil {
		t.Fatalf("review task lookup: %v", err)
	}
	if reviewTask.RequiredRole != "reviewer" {
		t.Fatalf("review task required_role: %q want reviewer", reviewTask.RequiredRole)
	}
	if reviewTask.ContractInstanceID != f.cis[0].ID {
		t.Fatalf("review task ci: %q want %q", reviewTask.ContractInstanceID, f.cis[0].ID)
	}

	// Now exercise contract_review_close — accepted path.
	revRes, err := f.server.handleContractReviewClose(f.callerCtx(), newCallToolReq("contract_review_close", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"verdict":              "accepted",
		"rationale":            "AC mapped, plan complete",
		"review_task_id":       body.ReviewTaskID,
	}))
	if err != nil || revRes.IsError {
		t.Fatalf("review_close: err=%v text=%s", err, firstText(revRes))
	}

	finalCI, _ := f.server.contracts.GetByID(f.ctx, f.cis[0].ID, nil)
	if finalCI.Status != contract.StatusPassed {
		t.Fatalf("CI status after review_close: %q want passed", finalCI.Status)
	}
	finalTask, _ := taskStore.GetByID(f.ctx, body.ReviewTaskID, nil)
	if finalTask.Status != task.StatusClosed {
		t.Fatalf("review task status: %q want closed", finalTask.Status)
	}
}

// TestReviewClose_RejectsNonPendingReview covers the guard: review_close
// called on a CI not in pending_review state returns
// ci_not_pending_review.
func TestReviewClose_RejectsNonPendingReview(t *testing.T) {
	t.Parallel()
	f := newCloseFixture(t)
	// f.cis[0] is in StatusReady — definitely not pending_review.
	res, _ := f.server.handleContractReviewClose(f.callerCtx(), newCallToolReq("contract_review_close", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"verdict":              "accepted",
	}))
	if !res.IsError {
		t.Fatalf("expected error; got %s", firstText(res))
	}
	if !strings.Contains(firstText(res), "ci_not_pending_review") {
		t.Fatalf("expected ci_not_pending_review; got %s", firstText(res))
	}
}

// TestReviewClose_RejectsNeedsMore covers the guard: review_close only
// accepts {accepted, rejected}; needs_more is rejected because
// pending_review → claimed isn't a valid back-transition.
func TestReviewClose_RejectsNeedsMore(t *testing.T) {
	t.Parallel()
	f := newCloseFixture(t)
	res, _ := f.server.handleContractReviewClose(f.callerCtx(), newCallToolReq("contract_review_close", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"verdict":              "needs_more",
	}))
	if !res.IsError {
		t.Fatalf("expected error; got %s", firstText(res))
	}
	if !strings.Contains(firstText(res), "invalid_verdict") {
		t.Fatalf("expected invalid_verdict; got %s", firstText(res))
	}
}

// TestReviewClose_RejectAppendsFreshCI covers
// epic:v4-lifecycle-refactor sty_bbe732af — verdict=rejected appends a
// fresh CI of the same contract type with PriorCIID set so the next
// claimer inherits the prior attempt's evidence + the rejection
// rationale via standard ledger reads.
func TestReviewClose_RejectAppendsFreshCI(t *testing.T) {
	t.Parallel()
	f := newCloseFixture(t)
	taskStore := task.NewMemoryStore()
	f.server.tasks = taskStore

	// Mark the plan contract validation_mode=task.
	planContractID := f.cis[0].ContractID
	doc, _ := f.server.docs.GetByID(f.ctx, planContractID, nil)
	taskMode, _ := json.Marshal(map[string]any{"validation_mode": "task"})
	if _, err := f.server.docs.Update(f.ctx, doc.ID, document.UpdateFields{Structured: &taskMode}, "test", f.now, nil); err != nil {
		t.Fatalf("update doc: %v", err)
	}

	// Plan-close gate — enqueue a child task first.
	if _, err := taskStore.Enqueue(f.ctx, task.Task{
		WorkspaceID:        f.wsID,
		ContractInstanceID: f.cis[0].ID,
		RequiredRole:       "developer",
		Origin:             task.OriginStoryStage,
		Priority:           task.PriorityMedium,
	}, f.now); err != nil {
		t.Fatalf("enqueue child: %v", err)
	}

	f.claim(t, 0, "v1 plan")
	closeRes, _ := f.server.handleContractClose(f.callerCtx(), newCallToolReq("contract_close", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"close_markdown":       "v1 close",
	}))
	var closeBody struct {
		ReviewTaskID string `json:"review_task_id"`
	}
	_ = json.Unmarshal([]byte(firstText(closeRes)), &closeBody)

	// Reject — should append a fresh plan CI with PriorCIID set.
	revRes, _ := f.server.handleContractReviewClose(f.callerCtx(), newCallToolReq("contract_review_close", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"verdict":              "rejected",
		"rationale":            "AC2 not addressed",
		"review_task_id":       closeBody.ReviewTaskID,
	}))
	if revRes.IsError {
		t.Fatalf("review_close: %s", firstText(revRes))
	}
	var revBody struct {
		Status        string `json:"status"`
		AppendedCIID  string `json:"appended_ci_id"`
		BlockedReason string `json:"blocked_reason"`
	}
	_ = json.Unmarshal([]byte(firstText(revRes)), &revBody)
	if revBody.Status != contract.StatusFailed {
		t.Fatalf("status: %q want failed", revBody.Status)
	}
	if revBody.AppendedCIID == "" {
		t.Fatalf("expected appended_ci_id (iteration 2 of plan)")
	}
	if revBody.BlockedReason != "" {
		t.Fatalf("unexpected blocked_reason: %q", revBody.BlockedReason)
	}
	appended, err := f.server.contracts.GetByID(f.ctx, revBody.AppendedCIID, nil)
	if err != nil {
		t.Fatalf("appended CI lookup: %v", err)
	}
	if appended.PriorCIID != f.cis[0].ID {
		t.Fatalf("PriorCIID: got %q want %q", appended.PriorCIID, f.cis[0].ID)
	}
	if appended.ContractName != "plan" {
		t.Fatalf("appended ContractName: %q want plan", appended.ContractName)
	}
	if appended.Status != contract.StatusReady {
		t.Fatalf("appended Status: %q want ready", appended.Status)
	}
}

// TestReviewClose_IterationCapBlocksStory covers cap behavior — when
// the third rejection fires for the same contract type, the story is
// flipped to blocked and no further CI is appended.
func TestReviewClose_IterationCapBlocksStory(t *testing.T) {
	t.Setenv("SATELLITES_REVIEW_ITERATION_CAP", "2")

	f := newCloseFixture(t)
	taskStore := task.NewMemoryStore()
	f.server.tasks = taskStore

	// Move story to in_progress so it's a valid source for the
	// blocked transition.
	if _, err := f.server.stories.UpdateStatus(f.ctx, f.storyID, "ready", f.caller.UserID, f.now, nil); err != nil {
		t.Fatalf("story->ready: %v", err)
	}
	if _, err := f.server.stories.UpdateStatus(f.ctx, f.storyID, "in_progress", f.caller.UserID, f.now, nil); err != nil {
		t.Fatalf("story->in_progress: %v", err)
	}

	// Mark the plan contract validation_mode=task.
	doc, _ := f.server.docs.GetByID(f.ctx, f.cis[0].ContractID, nil)
	taskMode, _ := json.Marshal(map[string]any{"validation_mode": "task"})
	if _, err := f.server.docs.Update(f.ctx, doc.ID, document.UpdateFields{Structured: &taskMode}, "test", f.now, nil); err != nil {
		t.Fatalf("update doc: %v", err)
	}

	// Seed two prior plan CIs at the same contract slot so the existing
	// f.cis[0] is the *second* iteration. The cap (2) means the next
	// rejection on f.cis[0] hits the cap.
	if _, err := f.server.contracts.Create(f.ctx, contract.ContractInstance{
		WorkspaceID: f.wsID, ProjectID: f.projectID, StoryID: f.storyID,
		ContractID: f.cis[0].ContractID, ContractName: "plan",
		Sequence: 0, Status: contract.StatusFailed,
	}, f.now); err != nil {
		t.Fatalf("seed prior plan ci: %v", err)
	}

	// Plan-close gate — task linked.
	if _, err := taskStore.Enqueue(f.ctx, task.Task{
		WorkspaceID:        f.wsID,
		ContractInstanceID: f.cis[0].ID,
		RequiredRole:       "developer",
		Origin:             task.OriginStoryStage,
		Priority:           task.PriorityMedium,
	}, f.now); err != nil {
		t.Fatalf("enqueue child: %v", err)
	}
	f.claim(t, 0, "v2")
	_, _ = f.server.handleContractClose(f.callerCtx(), newCallToolReq("contract_close", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"close_markdown":       "v2",
	}))
	revRes, _ := f.server.handleContractReviewClose(f.callerCtx(), newCallToolReq("contract_review_close", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"verdict":              "rejected",
		"rationale":            "still missing AC2",
	}))
	if revRes.IsError {
		t.Fatalf("review_close: %s", firstText(revRes))
	}
	var revBody struct {
		AppendedCIID  string `json:"appended_ci_id"`
		BlockedReason string `json:"blocked_reason"`
	}
	_ = json.Unmarshal([]byte(firstText(revRes)), &revBody)
	if revBody.AppendedCIID != "" {
		t.Fatalf("cap exceeded — should not append new CI; got %q", revBody.AppendedCIID)
	}
	if revBody.BlockedReason == "" {
		t.Fatalf("expected blocked_reason on cap exceeded")
	}
	st, _ := f.server.stories.GetByID(f.ctx, f.storyID, nil)
	if st.Status != "blocked" {
		t.Fatalf("story status: %q want blocked", st.Status)
	}
}
