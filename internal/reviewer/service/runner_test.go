package service_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/reviewer"
	"github.com/bobmcallan/satellites/internal/reviewer/service"
	"github.com/bobmcallan/satellites/internal/task"
)

// stubReviewer returns a fixed verdict (and an optional error). Used
// to drive the service with deterministic outcomes — the real
// gemini reviewer requires network.
type stubReviewer struct {
	verdict reviewer.Verdict
	err     error
	calls   atomic.Int32
}

func (s *stubReviewer) Review(ctx context.Context, req reviewer.Request) (reviewer.Verdict, reviewer.UsageCost, error) {
	s.calls.Add(1)
	if s.err != nil {
		return reviewer.Verdict{}, reviewer.UsageCost{}, s.err
	}
	return s.verdict, reviewer.UsageCost{}, nil
}

// fixture wires the in-memory stores and seeds a parent kind:work task
// + paired kind:review task referencing it via ParentTaskID. sty_c6d76a5b
// checkpoint 14: contract.Store is gone; the reviewer service reads
// everything it needs from the task + the ledger.
type fixture struct {
	tasks         task.Store
	docs          document.Store
	ledger        ledger.Store
	reviewerAgent document.Document
	contractDoc   document.Document
	storyID       string
	workspaceID   string
	projectID     string
	parentWork    task.Task
	reviewTask    task.Task
	reviewer      *stubReviewer
}

func newFixture(t *testing.T, verdict reviewer.Verdict, reviewerErr error) *fixture {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	docs := document.NewMemoryStore()
	tasks := task.NewMemoryStore()
	led := ledger.NewMemoryStore()

	contractDoc, err := docs.Create(ctx, document.Document{
		WorkspaceID: "wksp_a",
		Type:        document.TypeContract,
		Name:        "develop",
		Body:        "develop contract instruction",
		Scope:       document.ScopeSystem,
		Status:      document.StatusActive,
	}, now)
	require.NoError(t, err)

	rubric, err := docs.Create(ctx, document.Document{
		WorkspaceID: "wksp_a",
		Type:        document.TypeAgent,
		Name:        "development_reviewer",
		Body:        "review for develop",
		Scope:       document.ScopeSystem,
		Status:      document.StatusActive,
	}, now)
	require.NoError(t, err)

	storyID := "sty_a1"
	workspaceID := "wksp_a"
	projectID := "proj_a"

	parentWork, err := tasks.Enqueue(ctx, task.Task{
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
		StoryID:     storyID,
		Kind:        task.KindWork,
		Action:      task.ContractAction("develop"),
		AgentID:     "agent_developer",
		Description: "implement develop",
		Origin:      task.OriginStoryStage,
		Priority:    task.PriorityMedium,
		Status:      task.StatusPublished,
	}, now)
	require.NoError(t, err)
	parentWork, err = tasks.Close(ctx, parentWork.ID, task.OutcomeSuccess, now, nil)
	require.NoError(t, err)

	// Seed an evidence ledger row tagged to the parent work task — the
	// reviewer service should pick this up via task_id:<parent.ID>.
	_, err = led.Append(ctx, ledger.LedgerEntry{
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
		StoryID:     ledger.StringPtr(storyID),
		Type:        ledger.TypeEvidence,
		Tags:        []string{"kind:evidence", "task_id:" + parentWork.ID},
		Content:     "developer evidence body",
	}, now)
	require.NoError(t, err)

	reviewTask, err := tasks.Enqueue(ctx, task.Task{
		WorkspaceID:  workspaceID,
		ProjectID:    projectID,
		StoryID:      storyID,
		Kind:         task.KindReview,
		Action:       task.ContractAction("develop"),
		AgentID:      rubric.ID,
		ParentTaskID: parentWork.ID,
		Origin:       task.OriginStoryStage,
		Priority:     task.PriorityMedium,
	}, now)
	require.NoError(t, err)

	return &fixture{
		tasks:         tasks,
		docs:          docs,
		ledger:        led,
		reviewerAgent: rubric,
		contractDoc:   contractDoc,
		storyID:       storyID,
		workspaceID:   workspaceID,
		projectID:     projectID,
		parentWork:    parentWork,
		reviewTask:    reviewTask,
		reviewer:      &stubReviewer{verdict: verdict, err: reviewerErr},
	}
}

func (f *fixture) newService(t *testing.T) *service.Service {
	t.Helper()
	svc, err := service.New(service.Config{}, service.Deps{
		Tasks:    f.tasks,
		Docs:     f.docs,
		Ledger:   f.ledger,
		Reviewer: f.reviewer,
	})
	require.NoError(t, err)
	return svc
}

// findVerdictRow returns the kind:verdict ledger row tagged to the
// given review task id, failing the test if exactly one row is not
// found.
func findVerdictRow(t *testing.T, led ledger.Store, taskID string) ledger.LedgerEntry {
	t.Helper()
	rows, err := led.List(context.Background(), "", ledger.ListOptions{
		Type: ledger.TypeVerdict,
		Tags: []string{"kind:verdict", "task_id:" + taskID},
	}, nil)
	require.NoError(t, err)
	require.Lenf(t, rows, 1, "expected exactly one kind:verdict row tagged task_id:%s, got %d", taskID, len(rows))
	return rows[0]
}

// findSuccessorWork returns the kind:work task whose PriorTaskID
// matches priorID, failing if not exactly one.
func findSuccessorWork(t *testing.T, tasks task.Store, priorID string) task.Task {
	t.Helper()
	rows, err := tasks.List(context.Background(), task.ListOptions{
		Kind: task.KindWork,
	}, nil)
	require.NoError(t, err)
	matches := make([]task.Task, 0)
	for _, r := range rows {
		if r.PriorTaskID == priorID {
			matches = append(matches, r)
		}
	}
	require.Lenf(t, matches, 1, "expected exactly one kind:work successor with PriorTaskID=%s, got %d", priorID, len(matches))
	return matches[0]
}

func TestService_Tick_HappyPath_AcceptedCommit(t *testing.T) {
	t.Parallel()
	f := newFixture(t, reviewer.Verdict{Outcome: reviewer.VerdictAccepted, Rationale: "ok"}, nil)
	svc := f.newService(t)

	require.True(t, svc.HandleTaskEvent(context.Background(), f.reviewTask), "expected the service to claim and process the review task")

	assert.Equal(t, int32(1), f.reviewer.calls.Load(), "reviewer should be invoked exactly once")

	row := findVerdictRow(t, f.ledger, f.reviewTask.ID)
	assert.Equal(t, "ok", row.Content)
	assert.Contains(t, row.Tags, "kind:verdict")
	assert.Contains(t, row.Tags, "task_id:"+f.reviewTask.ID)
	assert.Contains(t, row.Tags, "phase:develop")

	closed, err := f.tasks.GetByID(context.Background(), f.reviewTask.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, task.StatusClosed, closed.Status)
	assert.Equal(t, task.OutcomeSuccess, closed.Outcome)

	// Accepted path must NOT spawn a successor work task.
	rows, err := f.tasks.List(context.Background(), task.ListOptions{Kind: task.KindWork}, nil)
	require.NoError(t, err)
	for _, r := range rows {
		assert.NotEqual(t, f.parentWork.ID, r.PriorTaskID, "accepted verdict must not spawn a successor for parent work %s", f.parentWork.ID)
	}
}

func TestService_Tick_RejectedCommit_SpawnsSuccessorPair(t *testing.T) {
	t.Parallel()
	f := newFixture(t, reviewer.Verdict{Outcome: reviewer.VerdictRejected, Rationale: "missing evidence"}, nil)
	svc := f.newService(t)

	require.True(t, svc.HandleTaskEvent(context.Background(), f.reviewTask))

	row := findVerdictRow(t, f.ledger, f.reviewTask.ID)
	assert.Contains(t, row.Content, "missing evidence")

	closed, err := f.tasks.GetByID(context.Background(), f.reviewTask.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, task.StatusClosed, closed.Status)
	assert.Equal(t, task.OutcomeFailure, closed.Outcome)

	successor := findSuccessorWork(t, f.tasks, f.parentWork.ID)
	assert.Equal(t, task.KindWork, successor.Kind)
	assert.Equal(t, f.parentWork.Action, successor.Action)
	assert.Equal(t, f.parentWork.AgentID, successor.AgentID)
	assert.Equal(t, task.StatusPublished, successor.Status, "successor work task must be claimable")

	// The paired planned-review for the successor must exist.
	allReviews, err := f.tasks.List(context.Background(), task.ListOptions{Kind: task.KindReview}, nil)
	require.NoError(t, err)
	plannedSibling := 0
	for _, r := range allReviews {
		if r.ParentTaskID == successor.ID && r.Status == task.StatusPlanned {
			plannedSibling++
		}
	}
	assert.Equal(t, 1, plannedSibling, "successor work must have exactly one planned-review sibling")
}

func TestService_Tick_RejectedCommit_NoParentSkipsSpawn(t *testing.T) {
	t.Parallel()
	f := newFixture(t, reviewer.Verdict{Outcome: reviewer.VerdictRejected, Rationale: "legacy review"}, nil)
	// Simulate a legacy review task that lacks the ParentTaskID anchor.
	legacyReview, err := f.tasks.Enqueue(context.Background(), task.Task{
		WorkspaceID: f.workspaceID,
		ProjectID:   f.projectID,
		StoryID:     f.storyID,
		Kind:        task.KindReview,
		Action:      task.ContractAction("develop"),
		Origin:      task.OriginStoryStage,
		Priority:    task.PriorityMedium,
	}, time.Now().UTC())
	require.NoError(t, err)

	svc := f.newService(t)
	require.True(t, svc.HandleTaskEvent(context.Background(), legacyReview))

	closed, err := f.tasks.GetByID(context.Background(), legacyReview.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, task.StatusClosed, closed.Status)
	assert.Equal(t, task.OutcomeFailure, closed.Outcome)

	// No successor for legacy review (no ParentTaskID anchor).
	rows, err := f.tasks.List(context.Background(), task.ListOptions{Kind: task.KindWork}, nil)
	require.NoError(t, err)
	for _, r := range rows {
		assert.Empty(t, r.PriorTaskID, "legacy review without ParentTaskID must not spawn a successor (got %s with prior=%s)", r.ID, r.PriorTaskID)
	}
}

func TestService_Tick_NeedsMore_TreatedAsRejected(t *testing.T) {
	t.Parallel()
	f := newFixture(t, reviewer.Verdict{
		Outcome:         reviewer.VerdictNeedsMore,
		Rationale:       "incomplete",
		ReviewQuestions: []string{"q1", "q2"},
	}, nil)
	svc := f.newService(t)

	require.True(t, svc.HandleTaskEvent(context.Background(), f.reviewTask))
	row := findVerdictRow(t, f.ledger, f.reviewTask.ID)
	assert.Contains(t, row.Content, "q1")
	assert.Contains(t, row.Content, "q2")

	// Each question must also land as its own kind:review-question
	// ledger row tagged with the parent work task's id. ledger.List
	// tags filter is OR — list by kind, then assert the parent task tag
	// is present on every match.
	rows, err := f.ledger.List(context.Background(), "", ledger.ListOptions{
		Tags: []string{"kind:review-question"},
	}, nil)
	require.NoError(t, err)
	require.Len(t, rows, 2, "needs_more must post one kind:review-question row per question")
	contents := []string{}
	for _, r := range rows {
		contents = append(contents, r.Content)
		assert.Contains(t, r.Tags, "task_id:"+f.parentWork.ID, "review-question rows must carry parent work task id")
	}
	assert.Contains(t, contents, "q1")
	assert.Contains(t, contents, "q2")

	// needs_more is coerced to rejected → successor spawns.
	_ = findSuccessorWork(t, f.tasks, f.parentWork.ID)
}

func TestService_Tick_GeminiError_RejectsWithRationale(t *testing.T) {
	t.Parallel()
	f := newFixture(t, reviewer.Verdict{}, errors.New("gemini api timeout"))
	svc := f.newService(t)

	require.True(t, svc.HandleTaskEvent(context.Background(), f.reviewTask))

	row := findVerdictRow(t, f.ledger, f.reviewTask.ID)
	assert.Contains(t, row.Content, "reviewer error")
	assert.Contains(t, row.Content, "gemini api timeout")

	closed, err := f.tasks.GetByID(context.Background(), f.reviewTask.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, task.StatusClosed, closed.Status)
	assert.Equal(t, task.OutcomeFailure, closed.Outcome)
}

// TestService_HandleTaskEvent_NotFound covers HandleTaskEvent's
// race-loss path: when the task referenced by an emit is gone (or
// never existed) the service returns false without invoking the
// reviewer.
func TestService_HandleTaskEvent_NotFound(t *testing.T) {
	t.Parallel()
	docs := document.NewMemoryStore()
	tasks := task.NewMemoryStore()
	led := ledger.NewMemoryStore()
	rev := &stubReviewer{verdict: reviewer.Verdict{Outcome: reviewer.VerdictAccepted}}

	svc, err := service.New(service.Config{}, service.Deps{
		Tasks:    tasks,
		Docs:     docs,
		Ledger:   led,
		Reviewer: rev,
	})
	require.NoError(t, err)

	missing := task.Task{ID: "task_deadbeef", WorkspaceID: "wksp_a", Kind: task.KindReview}
	assert.False(t, svc.HandleTaskEvent(context.Background(), missing), "missing task → service skips silently")
	assert.Equal(t, int32(0), rev.calls.Load(), "reviewer must not be invoked when the task is missing")
}

// TestService_OnEmit_KindFilter verifies the listener filter: emits
// for non-review tasks (or review tasks at non-claimable status) are
// silently ignored.
func TestService_OnEmit_KindFilter(t *testing.T) {
	t.Parallel()
	f := newFixture(t, reviewer.Verdict{Outcome: reviewer.VerdictAccepted}, nil)
	ctx := context.Background()

	other, err := f.tasks.Enqueue(ctx, task.Task{
		WorkspaceID: f.workspaceID,
		ProjectID:   f.projectID,
		StoryID:     f.storyID,
		Kind:        task.KindWork,
		Origin:      task.OriginStoryStage,
		Priority:    task.PriorityCritical,
	}, time.Now().UTC())
	require.NoError(t, err)

	svc := f.newService(t)

	svc.OnEmit(ctx, other)
	assert.Equal(t, int32(0), f.reviewer.calls.Load(), "reviewer must not be invoked on kind=work emits")

	svc.OnEmit(ctx, f.reviewTask)
	assert.Equal(t, int32(1), f.reviewer.calls.Load(), "reviewer should be invoked exactly once on kind=review emit")

	otherAfter, err := f.tasks.GetByID(ctx, other.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, task.StatusEnqueued, otherAfter.Status)
	reviewAfter, err := f.tasks.GetByID(ctx, f.reviewTask.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, task.StatusClosed, reviewAfter.Status)
	assert.Equal(t, task.OutcomeSuccess, reviewAfter.Outcome)
}

func TestService_Run_ContextCancellationStops(t *testing.T) {
	t.Parallel()
	f := newFixture(t, reviewer.Verdict{Outcome: reviewer.VerdictAccepted}, nil)
	svc := f.newService(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()

	require.Eventually(t, func() bool {
		return f.reviewer.calls.Load() == 1
	}, time.Second, 10*time.Millisecond, "service should claim the seeded task within 1 s")

	cancel()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("Run did not exit after cancellation")
	}
}

func TestService_New_RejectsMissingDeps(t *testing.T) {
	t.Parallel()
	docs := document.NewMemoryStore()
	tasks := task.NewMemoryStore()
	led := ledger.NewMemoryStore()
	rev := &stubReviewer{}

	cases := []struct {
		name string
		deps service.Deps
	}{
		{"missing tasks", service.Deps{Docs: docs, Ledger: led, Reviewer: rev}},
		{"missing docs", service.Deps{Tasks: tasks, Ledger: led, Reviewer: rev}},
		{"missing ledger", service.Deps{Tasks: tasks, Docs: docs, Reviewer: rev}},
		{"missing reviewer", service.Deps{Tasks: tasks, Docs: docs, Ledger: led}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := service.New(service.Config{}, tc.deps)
			require.Error(t, err)
		})
	}
}
