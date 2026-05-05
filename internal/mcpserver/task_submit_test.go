package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/task"
)

// minimalPlanTasks returns the smallest list that passes
// validatePlanTasks: a paired plan+plan_review pair followed by a
// paired story_close+story_close_review pair. Used as a baseline that
// individual test cases mutate to provoke specific rejection classes.
func minimalPlanTasks() []taskInput {
	return []taskInput{
		{Kind: task.KindWork, Action: task.ContractAction("plan"), Description: "draft the plan"},
		{Kind: task.KindReview, Action: task.ContractAction("plan"), Description: "review the plan"},
		{Kind: task.KindWork, Action: task.ContractAction("story_close"), Description: "close the story"},
		{Kind: task.KindReview, Action: task.ContractAction("story_close"), Description: "review the close"},
	}
}

func tasksJSON(t *testing.T, inputs []taskInput) string {
	t.Helper()
	b, err := json.Marshal(inputs)
	if err != nil {
		t.Fatalf("marshal tasks: %v", err)
	}
	return string(b)
}

// TestTaskSubmit_HappyPath_PlanKind covers the happy path:
// agent-authored task list passes structural validation, tasks are
// persisted with story_id linkage and the right kind/action fields,
// kind:plan ledger row is written, response carries task_ids.
func TestTaskSubmit_HappyPath_PlanKind(t *testing.T) {
	t.Parallel()
	f := newOrchestratorFixture(t)
	inputs := minimalPlanTasks()

	res, err := f.server.handleTaskSubmit(f.callerCtx(), newCallToolReq("task_submit", map[string]any{
		"story_id": f.storyID,
		"kind":     SubmitKindPlan,
		"tasks":    tasksJSON(t, inputs),
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if res.IsError {
		t.Fatalf("handler returned error: %s", firstText(res))
	}

	var body struct {
		StoryID      string   `json:"story_id"`
		PlanLedgerID string   `json:"plan_ledger_id"`
		TaskIDs      []string `json:"task_ids"`
		TaskCount    int      `json:"task_count"`
	}
	if err := json.Unmarshal([]byte(firstText(res)), &body); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if body.StoryID != f.storyID {
		t.Errorf("story_id: got %q want %q", body.StoryID, f.storyID)
	}
	if body.PlanLedgerID == "" {
		t.Error("plan_ledger_id empty")
	}
	if body.TaskCount != len(inputs) {
		t.Errorf("task_count: got %d want %d", body.TaskCount, len(inputs))
	}
	if len(body.TaskIDs) != len(inputs) {
		t.Fatalf("task_ids len: got %d want %d", len(body.TaskIDs), len(inputs))
	}
	for i, id := range body.TaskIDs {
		got, gerr := f.taskStore.GetByID(context.Background(), id, []string{f.wsID})
		if gerr != nil {
			t.Fatalf("task[%d] %s: %v", i, id, gerr)
		}
		if got.StoryID != f.storyID {
			t.Errorf("task[%d] story_id: got %q want %q", i, got.StoryID, f.storyID)
		}
		if got.Action != inputs[i].Action {
			t.Errorf("task[%d] action: got %q want %q", i, got.Action, inputs[i].Action)
		}
		if got.Kind != inputs[i].Kind {
			t.Errorf("task[%d] kind: got %q want %q", i, got.Kind, inputs[i].Kind)
		}
		if got.Description != inputs[i].Description {
			t.Errorf("task[%d] description: got %q want %q", i, got.Description, inputs[i].Description)
		}
		// Work tasks land at published (claimable now); review tasks
		// land at planned (subscriber-invisible until the matching
		// work task closes).
		wantStatus := task.StatusPublished
		if inputs[i].Kind == task.KindReview {
			wantStatus = task.StatusPlanned
		}
		if got.Status != wantStatus {
			t.Errorf("task[%d] (%s/%s) status: got %q want %q", i, inputs[i].Kind, inputs[i].Action, got.Status, wantStatus)
		}
	}
}

// TestTaskSubmit_Idempotent verifies a second submission against
// a story that already has tasks returns the existing ids without
// duplicating rows.
func TestTaskSubmit_Idempotent(t *testing.T) {
	t.Parallel()
	f := newOrchestratorFixture(t)
	args := map[string]any{
		"story_id": f.storyID,
		"kind":     SubmitKindPlan,
		"tasks":    tasksJSON(t, minimalPlanTasks()),
	}

	first, err := f.server.handleTaskSubmit(f.callerCtx(), newCallToolReq("task_submit", args))
	if err != nil || first.IsError {
		t.Fatalf("first submit: err=%v body=%s", err, firstText(first))
	}
	second, err := f.server.handleTaskSubmit(f.callerCtx(), newCallToolReq("task_submit", args))
	if err != nil || second.IsError {
		t.Fatalf("second submit: err=%v body=%s", err, firstText(second))
	}
	text := firstText(second)
	if !strings.Contains(text, `"idempotent":true`) {
		t.Errorf("second response missing idempotent flag: %s", text)
	}

	all, err := f.taskStore.List(f.ctx, task.ListOptions{StoryID: f.storyID}, []string{f.wsID})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(all) != len(minimalPlanTasks()) {
		t.Errorf("task count after idempotent re-submit: got %d want %d", len(all), len(minimalPlanTasks()))
	}
}

// TestTaskSubmit_RejectsFirstNotPlan covers the
// `plan_first_task_must_be_plan` rejection class.
func TestTaskSubmit_RejectsFirstNotPlan(t *testing.T) {
	t.Parallel()
	f := newOrchestratorFixture(t)
	bad := []taskInput{
		{Kind: task.KindWork, Action: task.ContractAction("develop")},
		{Kind: task.KindReview, Action: task.ContractAction("develop")},
	}

	res, err := f.server.handleTaskSubmit(f.callerCtx(), newCallToolReq("task_submit", map[string]any{
		"story_id": f.storyID,
		"kind":     SubmitKindPlan,
		"tasks":    tasksJSON(t, bad),
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected rejection; got: %s", firstText(res))
	}
	if !strings.Contains(firstText(res), "plan_first_task_must_be_plan") {
		t.Errorf("unexpected rejection: %s", firstText(res))
	}
}

// TestTaskSubmit_RejectsMissingReview covers the
// `missing_review_for:` rejection class — the substrate refuses to
// silently insert a review task; the agent owns the list.
func TestTaskSubmit_RejectsMissingReview(t *testing.T) {
	t.Parallel()
	f := newOrchestratorFixture(t)
	bad := []taskInput{
		{Kind: task.KindWork, Action: task.ContractAction("plan")},
		{Kind: task.KindReview, Action: task.ContractAction("plan")},
		{Kind: task.KindWork, Action: task.ContractAction("develop")},
		// Missing review for develop — story_close work follows
		// directly with no review for develop in between.
		{Kind: task.KindWork, Action: task.ContractAction("story_close")},
		{Kind: task.KindReview, Action: task.ContractAction("story_close")},
	}

	res, err := f.server.handleTaskSubmit(f.callerCtx(), newCallToolReq("task_submit", map[string]any{
		"story_id": f.storyID,
		"kind":     SubmitKindPlan,
		"tasks":    tasksJSON(t, bad),
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected rejection; got: %s", firstText(res))
	}
	if !strings.Contains(firstText(res), "missing_review_for") {
		t.Errorf("unexpected rejection text: %s", firstText(res))
	}
}

// TestTaskSubmit_RejectsReviewActionMismatch covers the
// `review_action_mismatch` rejection class — a review task whose
// action doesn't match the immediately preceding work task is
// rejected.
func TestTaskSubmit_RejectsReviewActionMismatch(t *testing.T) {
	t.Parallel()
	f := newOrchestratorFixture(t)
	bad := []taskInput{
		{Kind: task.KindWork, Action: task.ContractAction("plan")},
		// Review action is wrong contract.
		{Kind: task.KindReview, Action: task.ContractAction("develop")},
	}

	res, err := f.server.handleTaskSubmit(f.callerCtx(), newCallToolReq("task_submit", map[string]any{
		"story_id": f.storyID,
		"kind":     SubmitKindPlan,
		"tasks":    tasksJSON(t, bad),
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected rejection; got: %s", firstText(res))
	}
	if !strings.Contains(firstText(res), "review_action_mismatch") {
		t.Errorf("unexpected rejection text: %s", firstText(res))
	}
}

// TestTaskSubmit_RejectsBadActionFormat covers
// `invalid_action_format` — actions must use the `contract:<name>`
// canonical form.
func TestTaskSubmit_RejectsBadActionFormat(t *testing.T) {
	t.Parallel()
	f := newOrchestratorFixture(t)
	bad := []taskInput{
		{Kind: task.KindWork, Action: "plan"}, // missing `contract:` prefix
		{Kind: task.KindReview, Action: "plan"},
	}

	res, err := f.server.handleTaskSubmit(f.callerCtx(), newCallToolReq("task_submit", map[string]any{
		"story_id": f.storyID,
		"kind":     SubmitKindPlan,
		"tasks":    tasksJSON(t, bad),
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected rejection; got: %s", firstText(res))
	}
	// First-task-must-be-plan check fires before format check because
	// it compares against the canonical "contract:plan" string. Both
	// are valid rejections of this submission.
	got := firstText(res)
	if !strings.Contains(got, "plan_first_task_must_be_plan") &&
		!strings.Contains(got, "invalid_action_format") {
		t.Errorf("unexpected rejection text: %s", got)
	}
}

// TestTaskSubmit_RejectsEmptyTasks covers the empty-list case.
func TestTaskSubmit_RejectsEmptyTasks(t *testing.T) {
	t.Parallel()
	f := newOrchestratorFixture(t)

	res, err := f.server.handleTaskSubmit(f.callerCtx(), newCallToolReq("task_submit", map[string]any{
		"story_id": f.storyID,
		"kind":     SubmitKindPlan,
		"tasks":    "[]",
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected rejection; got: %s", firstText(res))
	}
	if !strings.Contains(firstText(res), "empty") {
		t.Errorf("unexpected rejection text: %s", firstText(res))
	}
}

// TestTaskSubmit_UnsupportedKind verifies the verb rejects
// kinds beyond the implemented set.
func TestTaskSubmit_UnsupportedKind(t *testing.T) {
	t.Parallel()
	f := newOrchestratorFixture(t)

	res, err := f.server.handleTaskSubmit(f.callerCtx(), newCallToolReq("task_submit", map[string]any{
		"story_id": f.storyID,
		"kind":     "spawn",
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected rejection; got: %s", firstText(res))
	}
	if !strings.Contains(firstText(res), "unsupported kind") {
		t.Errorf("unexpected rejection text: %s", firstText(res))
	}
}

// TestTaskSubmit_RejectsAgentCannotDeliver covers the
// `agent_cannot_deliver` rejection class — when a work task names an
// agent whose AgentSettings.Delivers list doesn't include the
// task's action.
func TestTaskSubmit_RejectsAgentCannotDeliver(t *testing.T) {
	t.Parallel()
	f := newOrchestratorFixture(t)
	// Find releaser_agent (only delivers push/merge_to_main, NOT plan).
	agents, err := f.server.docs.List(f.ctx, document.ListOptions{
		Type:  document.TypeAgent,
		Scope: document.ScopeSystem,
	}, nil)
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}
	var releaser document.Document
	for _, a := range agents {
		if a.Name == "releaser_agent" {
			releaser = a
			break
		}
	}
	if releaser.ID == "" {
		t.Fatal("releaser_agent not seeded")
	}

	bad := []taskInput{
		{Kind: task.KindWork, Action: task.ContractAction("plan"), AgentID: releaser.ID},
		{Kind: task.KindReview, Action: task.ContractAction("plan")},
	}
	res, err := f.server.handleTaskSubmit(f.callerCtx(), newCallToolReq("task_submit", map[string]any{
		"story_id": f.storyID,
		"kind":     SubmitKindPlan,
		"tasks":    tasksJSON(t, bad),
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected rejection")
	}
	if !strings.Contains(firstText(res), "agent_cannot_deliver") {
		t.Errorf("unexpected rejection text: %s", firstText(res))
	}
}

// TestTaskSubmit_RejectsAgentCannotReview covers the
// `agent_cannot_review` rejection class — when a review task names an
// agent whose AgentSettings.Reviews list doesn't include the action.
func TestTaskSubmit_RejectsAgentCannotReview(t *testing.T) {
	t.Parallel()
	f := newOrchestratorFixture(t)
	agents, err := f.server.docs.List(f.ctx, document.ListOptions{
		Type:  document.TypeAgent,
		Scope: document.ScopeSystem,
	}, nil)
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}
	// developer_agent has no Reviews list.
	var dev document.Document
	for _, a := range agents {
		if a.Name == "developer_agent" {
			dev = a
			break
		}
	}
	if dev.ID == "" {
		t.Fatal("developer_agent not seeded")
	}

	bad := []taskInput{
		{Kind: task.KindWork, Action: task.ContractAction("plan")},
		{Kind: task.KindReview, Action: task.ContractAction("plan"), AgentID: dev.ID},
	}
	res, err := f.server.handleTaskSubmit(f.callerCtx(), newCallToolReq("task_submit", map[string]any{
		"story_id": f.storyID,
		"kind":     SubmitKindPlan,
		"tasks":    tasksJSON(t, bad),
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected rejection")
	}
	if !strings.Contains(firstText(res), "agent_cannot_review") {
		t.Errorf("unexpected rejection text: %s", firstText(res))
	}
}

// submitMinimalPlan submits the baseline minimalPlanTasks() list
// against the fixture's story and returns the resulting task ids
// in submission order. Used by close tests to set up a story with
// a known task chain.
func submitMinimalPlan(t *testing.T, f *orchestratorFixture) []string {
	t.Helper()
	res, err := f.server.handleTaskSubmit(f.callerCtx(), newCallToolReq("task_submit", map[string]any{
		"story_id": f.storyID,
		"kind":     SubmitKindPlan,
		"tasks":    tasksJSON(t, minimalPlanTasks()),
	}))
	if err != nil || res.IsError {
		t.Fatalf("setup submit: err=%v body=%s", err, firstText(res))
	}
	var body struct {
		TaskIDs []string `json:"task_ids"`
	}
	if err := json.Unmarshal([]byte(firstText(res)), &body); err != nil {
		t.Fatalf("setup parse: %v", err)
	}
	return body.TaskIDs
}

// TestTaskSubmit_CloseKind_PublishesSiblingReview verifies the
// happy path for kind=close: closing a kind=work task publishes the
// kind=review sibling that was sitting at status=planned, so the
// reviewer service's subscribe path can claim it.
func TestTaskSubmit_CloseKind_PublishesSiblingReview(t *testing.T) {
	t.Parallel()
	f := newOrchestratorFixture(t)
	ids := submitMinimalPlan(t, f)
	planWorkID := ids[0]
	planReviewID := ids[1]

	// Sanity: review starts planned.
	rv, err := f.taskStore.GetByID(f.ctx, planReviewID, []string{f.wsID})
	if err != nil {
		t.Fatalf("review pre-check: %v", err)
	}
	if rv.Status != task.StatusPlanned {
		t.Fatalf("review pre-check status: got %q want %q", rv.Status, task.StatusPlanned)
	}

	res, err := f.server.handleTaskSubmit(f.callerCtx(), newCallToolReq("task_submit", map[string]any{
		"story_id": f.storyID,
		"kind":     SubmitKindClose,
		"task_id":  planWorkID,
		"outcome":  task.OutcomeSuccess,
	}))
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if res.IsError {
		t.Fatalf("close error: %s", firstText(res))
	}

	var body struct {
		TaskID            string `json:"task_id"`
		Status            string `json:"status"`
		Outcome           string `json:"outcome"`
		PublishedReviewID string `json:"published_review_id"`
	}
	if err := json.Unmarshal([]byte(firstText(res)), &body); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if body.Status != task.StatusClosed {
		t.Errorf("status: got %q want %q", body.Status, task.StatusClosed)
	}
	if body.Outcome != task.OutcomeSuccess {
		t.Errorf("outcome: got %q want %q", body.Outcome, task.OutcomeSuccess)
	}
	if body.PublishedReviewID != planReviewID {
		t.Errorf("published_review_id: got %q want %q", body.PublishedReviewID, planReviewID)
	}

	rv2, err := f.taskStore.GetByID(f.ctx, planReviewID, []string{f.wsID})
	if err != nil {
		t.Fatalf("review post-check: %v", err)
	}
	if rv2.Status != task.StatusPublished {
		t.Errorf("review post-check status: got %q want %q", rv2.Status, task.StatusPublished)
	}
}

// TestTaskSubmit_CloseKind_OnReviewTask doesn't publish anything
// — review tasks have no review sibling. Confirms the close path
// doesn't error when no sibling exists.
func TestTaskSubmit_CloseKind_OnReviewTask(t *testing.T) {
	t.Parallel()
	f := newOrchestratorFixture(t)
	ids := submitMinimalPlan(t, f)
	planWorkID, planReviewID := ids[0], ids[1]

	// Close the work first so the review is published and claimable.
	if _, err := f.taskStore.Close(f.ctx, planWorkID, task.OutcomeSuccess, f.now, []string{f.wsID}); err != nil {
		t.Fatalf("setup close work: %v", err)
	}
	if _, err := f.taskStore.Publish(f.ctx, planReviewID, f.now, []string{f.wsID}); err != nil {
		t.Fatalf("setup publish review: %v", err)
	}

	res, err := f.server.handleTaskSubmit(f.callerCtx(), newCallToolReq("task_submit", map[string]any{
		"story_id": f.storyID,
		"kind":     SubmitKindClose,
		"task_id":  planReviewID,
	}))
	if err != nil {
		t.Fatalf("close review: %v", err)
	}
	if res.IsError {
		t.Fatalf("close review error: %s", firstText(res))
	}
	var body struct {
		PublishedReviewID string `json:"published_review_id"`
	}
	if err := json.Unmarshal([]byte(firstText(res)), &body); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if body.PublishedReviewID != "" {
		t.Errorf("published_review_id should be empty when closing a review task, got %q", body.PublishedReviewID)
	}
}

// TestTaskSubmit_CloseKind_RejectsWrongStory verifies a task
// belonging to a different story is rejected.
func TestTaskSubmit_CloseKind_RejectsWrongStory(t *testing.T) {
	t.Parallel()
	f := newOrchestratorFixture(t)
	ids := submitMinimalPlan(t, f)

	// Make a second story; close the first story's task against it.
	otherStoryID := f.storyID + "_other"
	res, err := f.server.handleTaskSubmit(f.callerCtx(), newCallToolReq("task_submit", map[string]any{
		"story_id": otherStoryID,
		"kind":     SubmitKindClose,
		"task_id":  ids[0],
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected rejection")
	}
	// The story_id is unknown so the verb fails at story lookup
	// before reaching task_story_mismatch — both rejections are
	// acceptable.
	got := firstText(res)
	if !strings.Contains(got, "story not found") && !strings.Contains(got, "task_story_mismatch") {
		t.Errorf("unexpected rejection text: %s", got)
	}
}

// TestTaskSubmit_CloseKind_RejectsTerminal verifies closing an
// already-closed task is rejected, not silently accepted.
func TestTaskSubmit_CloseKind_RejectsTerminal(t *testing.T) {
	t.Parallel()
	f := newOrchestratorFixture(t)
	ids := submitMinimalPlan(t, f)
	planWorkID := ids[0]

	// First close — succeeds.
	first, err := f.server.handleTaskSubmit(f.callerCtx(), newCallToolReq("task_submit", map[string]any{
		"story_id": f.storyID,
		"kind":     SubmitKindClose,
		"task_id":  planWorkID,
	}))
	if err != nil || first.IsError {
		t.Fatalf("first close: err=%v body=%s", err, firstText(first))
	}

	// Second close — rejected.
	second, err := f.server.handleTaskSubmit(f.callerCtx(), newCallToolReq("task_submit", map[string]any{
		"story_id": f.storyID,
		"kind":     SubmitKindClose,
		"task_id":  planWorkID,
	}))
	if err != nil {
		t.Fatalf("second close handler: %v", err)
	}
	if !second.IsError {
		t.Fatalf("expected rejection on terminal task")
	}
	if !strings.Contains(firstText(second), "task_already_terminal") {
		t.Errorf("unexpected rejection text: %s", firstText(second))
	}
}

// TestTaskSubmit_CloseKind_RejectsBadOutcome verifies invalid
// outcome strings are rejected up-front.
func TestTaskSubmit_CloseKind_RejectsBadOutcome(t *testing.T) {
	t.Parallel()
	f := newOrchestratorFixture(t)
	ids := submitMinimalPlan(t, f)

	res, err := f.server.handleTaskSubmit(f.callerCtx(), newCallToolReq("task_submit", map[string]any{
		"story_id": f.storyID,
		"kind":     SubmitKindClose,
		"task_id":  ids[0],
		"outcome":  "bogus",
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected rejection")
	}
	if !strings.Contains(firstText(res), "invalid_outcome") {
		t.Errorf("unexpected rejection text: %s", firstText(res))
	}
}

// TestTaskSubmit_CloseKind_TaskNotFound verifies a missing
// task id is rejected cleanly.
func TestTaskSubmit_CloseKind_TaskNotFound(t *testing.T) {
	t.Parallel()
	f := newOrchestratorFixture(t)

	res, err := f.server.handleTaskSubmit(f.callerCtx(), newCallToolReq("task_submit", map[string]any{
		"story_id": f.storyID,
		"kind":     SubmitKindClose,
		"task_id":  "task_deadbeef",
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected rejection")
	}
	if !strings.Contains(firstText(res), "task_not_found") {
		t.Errorf("unexpected rejection text: %s", firstText(res))
	}
}
