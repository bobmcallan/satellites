package service_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/reviewer"
	"github.com/bobmcallan/satellites/internal/reviewer/service"
	"github.com/bobmcallan/satellites/internal/story"
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

// fixture wires the in-memory stores and seeds a CI in pending_review,
// a parent kind:work task, and a kind:review task linked to both
// (ContractInstanceID + ParentTaskID), so tests can assert on the full
// commit flow: verdict ledger row, CI status flip, review-task close,
// and successor-work spawn on rejection.
type fixture struct {
	tasks         task.Store
	contracts     contract.Store
	docs          document.Store
	ledger        ledger.Store
	stories       story.Store
	contractDoc   document.Document
	ci            contract.ContractInstance
	parentWork    task.Task
	reviewTask    task.Task
	reviewer      *stubReviewer
	reviewerAgent document.Document
	storyDoc      story.Story
}

func newFixture(t *testing.T, verdict reviewer.Verdict, reviewerErr error) *fixture {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	docs := document.NewMemoryStore()
	tasks := task.NewMemoryStore()
	led := ledger.NewMemoryStore()
	stories := story.NewMemoryStore(led)
	contracts := contract.NewMemoryStore(docs, stories)

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

	storyDoc, err := stories.Create(ctx, story.Story{
		WorkspaceID: "wksp_a",
		ProjectID:   "proj_a",
		Title:       "test story",
		Description: "test",
		Status:      story.StatusInProgress,
		Priority:    "medium",
		Category:    "feature",
		CreatedBy:   "system",
	}, now)
	require.NoError(t, err)

	ci, err := contracts.Create(ctx, contract.ContractInstance{
		WorkspaceID:      "wksp_a",
		ProjectID:        "proj_a",
		StoryID:          storyDoc.ID,
		ContractID:       contractDoc.ID,
		ContractName:     "develop",
		Sequence:         1,
		RequiredForClose: true,
		Status:           contract.StatusPendingReview,
	}, now)
	require.NoError(t, err)

	_, err = led.Append(ctx, ledger.LedgerEntry{
		WorkspaceID: "wksp_a",
		ProjectID:   "proj_a",
		ContractID:  ledger.StringPtr(ci.ID),
		Type:        ledger.TypeEvidence,
		Tags:        []string{"kind:evidence"},
		Content:     "developer evidence body",
	}, now)
	require.NoError(t, err)

	_, err = led.Append(ctx, ledger.LedgerEntry{
		WorkspaceID: "wksp_a",
		ProjectID:   "proj_a",
		ContractID:  ledger.StringPtr(ci.ID),
		Type:        ledger.TypeCloseRequest,
		Tags:        []string{"kind:close-request"},
		Content:     "developer close request",
	}, now)
	require.NoError(t, err)

	parentWork, err := tasks.Enqueue(ctx, task.Task{
		WorkspaceID:        "wksp_a",
		ProjectID:          "proj_a",
		StoryID:            storyDoc.ID,
		ContractInstanceID: ci.ID,
		Kind:               task.KindWork,
		Action:             task.ContractAction("develop"),
		AgentID:            "agent_developer",
		Description:        "implement develop",
		Origin:             task.OriginStoryStage,
		Priority:           task.PriorityMedium,
		Status:             task.StatusPublished,
	}, now)
	require.NoError(t, err)
	parentWork, err = tasks.Close(ctx, parentWork.ID, task.OutcomeSuccess, now, nil)
	require.NoError(t, err)

	reviewTask, err := tasks.Enqueue(ctx, task.Task{
		WorkspaceID:        "wksp_a",
		ProjectID:          "proj_a",
		StoryID:            storyDoc.ID,
		ContractInstanceID: ci.ID,
		Kind:               task.KindReview,
		Action:             task.ContractAction("develop"),
		AgentID:            rubric.ID,
		ParentTaskID:       parentWork.ID,
		Origin:             task.OriginStoryStage,
		Priority:           task.PriorityMedium,
	}, now)
	require.NoError(t, err)

	return &fixture{
		tasks:         tasks,
		contracts:     contracts,
		docs:          docs,
		ledger:        led,
		stories:       stories,
		contractDoc:   contractDoc,
		ci:            ci,
		parentWork:    parentWork,
		reviewTask:    reviewTask,
		reviewer:      &stubReviewer{verdict: verdict, err: reviewerErr},
		reviewerAgent: rubric,
		storyDoc:      storyDoc,
	}
}

func (f *fixture) newService(t *testing.T) *service.Service {
	t.Helper()
	svc, err := service.New(service.Config{}, service.Deps{
		Tasks:     f.tasks,
		Contracts: f.contracts,
		Docs:      f.docs,
		Ledger:    f.ledger,
		Reviewer:  f.reviewer,
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
	assert.Contains(t, row.Tags, "ci:"+f.ci.ID)

	closed, err := f.tasks.GetByID(context.Background(), f.reviewTask.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, task.StatusClosed, closed.Status)
	assert.Equal(t, task.OutcomeSuccess, closed.Outcome)

	ciAfter, err := f.contracts.GetByID(context.Background(), f.ci.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, contract.StatusPassed, ciAfter.Status, "accepted verdict must flip CI to passed (transitional)")

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

	ciAfter, err := f.contracts.GetByID(context.Background(), f.ci.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, contract.StatusFailed, ciAfter.Status, "rejected verdict must flip CI to failed (transitional)")

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
	// Simulate a legacy review task that lacks the ParentTaskID anchor
	// (created via contract_close before story_task_submit landed).
	legacyReview, err := f.tasks.Enqueue(context.Background(), task.Task{
		WorkspaceID:        "wksp_a",
		ProjectID:          "proj_a",
		StoryID:            f.storyDoc.ID,
		ContractInstanceID: f.ci.ID,
		Kind:               task.KindReview,
		Action:             task.ContractAction("develop"),
		Origin:             task.OriginStoryStage,
		Priority:           task.PriorityMedium,
	}, time.Now().UTC())
	require.NoError(t, err)

	svc := f.newService(t)
	require.True(t, svc.HandleTaskEvent(context.Background(), legacyReview))

	closed, err := f.tasks.GetByID(context.Background(), legacyReview.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, task.StatusClosed, closed.Status)
	assert.Equal(t, task.OutcomeFailure, closed.Outcome)

	// No successor for legacy review (no PriorTaskID anchor).
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
	// ledger row so contract_respond can address it (story_224621bd).
	rows, err := f.ledger.List(context.Background(), "", ledger.ListOptions{
		Tags: []string{"kind:review-question"},
	}, nil)
	require.NoError(t, err)
	require.Len(t, rows, 2, "needs_more must post one kind:review-question row per question")
	contents := []string{rows[0].Content, rows[1].Content}
	assert.Contains(t, contents, "q1")
	assert.Contains(t, contents, "q2")

	// needs_more is coerced to rejected → CI flips to failed and a
	// successor spawns.
	ciAfter, err := f.contracts.GetByID(context.Background(), f.ci.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, contract.StatusFailed, ciAfter.Status)
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
	stories := story.NewMemoryStore(led)
	contracts := contract.NewMemoryStore(docs, stories)
	rev := &stubReviewer{verdict: reviewer.Verdict{Outcome: reviewer.VerdictAccepted}}

	svc, err := service.New(service.Config{}, service.Deps{
		Tasks:     tasks,
		Contracts: contracts,
		Docs:      docs,
		Ledger:    led,
		Reviewer:  rev,
	})
	require.NoError(t, err)

	missing := task.Task{ID: "task_deadbeef", WorkspaceID: "wksp_a", Kind: task.KindReview}
	assert.False(t, svc.HandleTaskEvent(context.Background(), missing), "missing task → service skips silently")
	assert.Equal(t, int32(0), rev.calls.Load(), "reviewer must not be invoked when the task is missing")
}

// TestService_OnEmit_KindFilter verifies the listener filter: emits
// for non-review tasks (or review tasks at non-claimable status) are
// silently ignored. Under sty_c6d76a5b the substrate fans every emit
// out to every listener; the kind-filter is the listener's concern.
func TestService_OnEmit_KindFilter(t *testing.T) {
	t.Parallel()
	f := newFixture(t, reviewer.Verdict{Outcome: reviewer.VerdictAccepted}, nil)
	ctx := context.Background()

	// Inject a separate task with kind=work; the service must not
	// pick it up via OnEmit.
	other, err := f.tasks.Enqueue(ctx, task.Task{
		WorkspaceID:        "wksp_a",
		ProjectID:          "proj_a",
		ContractInstanceID: f.ci.ID,
		Kind:               task.KindWork,
		Origin:             task.OriginStoryStage,
		Priority:           task.PriorityCritical,
	}, time.Now().UTC())
	require.NoError(t, err)

	svc := f.newService(t)

	// Direct OnEmit with the kind=work task — listener must skip it.
	svc.OnEmit(ctx, other)
	assert.Equal(t, int32(0), f.reviewer.calls.Load(), "reviewer must not be invoked on kind=work emits")

	// Direct OnEmit with the seeded review task — listener processes it.
	svc.OnEmit(ctx, f.reviewTask)
	assert.Equal(t, int32(1), f.reviewer.calls.Load(), "reviewer should be invoked exactly once on kind=review emit")

	// Non-review task remains enqueued; review task got closed (the
	// commit path now closes it directly rather than leaving it
	// claimed for the legacy CommitFn to wrap up).
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

	// Give the loop a chance to claim the seeded task.
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
	stories := story.NewMemoryStore(led)
	contracts := contract.NewMemoryStore(docs, stories)
	rev := &stubReviewer{}

	cases := []struct {
		name string
		deps service.Deps
	}{
		{"missing tasks", service.Deps{Contracts: contracts, Docs: docs, Ledger: led, Reviewer: rev}},
		{"missing contracts", service.Deps{Tasks: tasks, Docs: docs, Ledger: led, Reviewer: rev}},
		{"missing docs", service.Deps{Tasks: tasks, Contracts: contracts, Ledger: led, Reviewer: rev}},
		{"missing ledger", service.Deps{Tasks: tasks, Contracts: contracts, Docs: docs, Reviewer: rev}},
		{"missing reviewer", service.Deps{Tasks: tasks, Contracts: contracts, Docs: docs, Ledger: led}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := service.New(service.Config{}, tc.deps)
			require.Error(t, err)
		})
	}
}
