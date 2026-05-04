package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

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

// TestStoryTaskSubmit_HappyPath_PlanKind covers the happy path:
// agent-authored task list passes structural validation, tasks are
// persisted with story_id linkage and the right kind/action fields,
// kind:plan ledger row is written, response carries task_ids.
func TestStoryTaskSubmit_HappyPath_PlanKind(t *testing.T) {
	t.Parallel()
	f := newOrchestratorFixture(t)
	inputs := minimalPlanTasks()

	res, err := f.server.handleStoryTaskSubmit(f.callerCtx(), newCallToolReq("story_task_submit", map[string]any{
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
		if got.Status != task.StatusPublished {
			t.Errorf("task[%d] status: got %q want %q", i, got.Status, task.StatusPublished)
		}
	}
}

// TestStoryTaskSubmit_Idempotent verifies a second submission against
// a story that already has tasks returns the existing ids without
// duplicating rows.
func TestStoryTaskSubmit_Idempotent(t *testing.T) {
	t.Parallel()
	f := newOrchestratorFixture(t)
	args := map[string]any{
		"story_id": f.storyID,
		"kind":     SubmitKindPlan,
		"tasks":    tasksJSON(t, minimalPlanTasks()),
	}

	first, err := f.server.handleStoryTaskSubmit(f.callerCtx(), newCallToolReq("story_task_submit", args))
	if err != nil || first.IsError {
		t.Fatalf("first submit: err=%v body=%s", err, firstText(first))
	}
	second, err := f.server.handleStoryTaskSubmit(f.callerCtx(), newCallToolReq("story_task_submit", args))
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

// TestStoryTaskSubmit_RejectsFirstNotPlan covers the
// `plan_first_task_must_be_plan` rejection class.
func TestStoryTaskSubmit_RejectsFirstNotPlan(t *testing.T) {
	t.Parallel()
	f := newOrchestratorFixture(t)
	bad := []taskInput{
		{Kind: task.KindWork, Action: task.ContractAction("develop")},
		{Kind: task.KindReview, Action: task.ContractAction("develop")},
	}

	res, err := f.server.handleStoryTaskSubmit(f.callerCtx(), newCallToolReq("story_task_submit", map[string]any{
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

// TestStoryTaskSubmit_RejectsMissingReview covers the
// `missing_review_for:` rejection class — the substrate refuses to
// silently insert a review task; the agent owns the list.
func TestStoryTaskSubmit_RejectsMissingReview(t *testing.T) {
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

	res, err := f.server.handleStoryTaskSubmit(f.callerCtx(), newCallToolReq("story_task_submit", map[string]any{
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

// TestStoryTaskSubmit_RejectsReviewActionMismatch covers the
// `review_action_mismatch` rejection class — a review task whose
// action doesn't match the immediately preceding work task is
// rejected.
func TestStoryTaskSubmit_RejectsReviewActionMismatch(t *testing.T) {
	t.Parallel()
	f := newOrchestratorFixture(t)
	bad := []taskInput{
		{Kind: task.KindWork, Action: task.ContractAction("plan")},
		// Review action is wrong contract.
		{Kind: task.KindReview, Action: task.ContractAction("develop")},
	}

	res, err := f.server.handleStoryTaskSubmit(f.callerCtx(), newCallToolReq("story_task_submit", map[string]any{
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

// TestStoryTaskSubmit_RejectsBadActionFormat covers
// `invalid_action_format` — actions must use the `contract:<name>`
// canonical form.
func TestStoryTaskSubmit_RejectsBadActionFormat(t *testing.T) {
	t.Parallel()
	f := newOrchestratorFixture(t)
	bad := []taskInput{
		{Kind: task.KindWork, Action: "plan"}, // missing `contract:` prefix
		{Kind: task.KindReview, Action: "plan"},
	}

	res, err := f.server.handleStoryTaskSubmit(f.callerCtx(), newCallToolReq("story_task_submit", map[string]any{
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

// TestStoryTaskSubmit_RejectsEmptyTasks covers the empty-list case.
func TestStoryTaskSubmit_RejectsEmptyTasks(t *testing.T) {
	t.Parallel()
	f := newOrchestratorFixture(t)

	res, err := f.server.handleStoryTaskSubmit(f.callerCtx(), newCallToolReq("story_task_submit", map[string]any{
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

// TestStoryTaskSubmit_UnsupportedKind verifies the verb rejects
// kinds beyond the implemented set.
func TestStoryTaskSubmit_UnsupportedKind(t *testing.T) {
	t.Parallel()
	f := newOrchestratorFixture(t)

	res, err := f.server.handleStoryTaskSubmit(f.callerCtx(), newCallToolReq("story_task_submit", map[string]any{
		"story_id": f.storyID,
		"kind":     "close",
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
