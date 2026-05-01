package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
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

// recordingCommit captures every commit call so tests can assert on
// the verdict path.
type recordingCommit struct {
	mu    sync.Mutex
	calls []commitCall
	err   error
}

type commitCall struct {
	CIID, Verdict, Rationale, TaskID, Actor string
}

func (r *recordingCommit) fn() service.CommitFn {
	return func(
		ctx context.Context,
		ciID, verdict, rationale, reviewTaskID, actor string,
		now time.Time,
		memberships []string,
	) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.calls = append(r.calls, commitCall{ciID, verdict, rationale, reviewTaskID, actor})
		return r.err
	}
}

func (r *recordingCommit) lastCall(t *testing.T) commitCall {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	require.NotEmpty(t, r.calls, "expected at least one commit call")
	return r.calls[len(r.calls)-1]
}

// fixture wires the in-memory stores and seeds a CI in pending_review
// + a kind:review task pointing at it, returning the task id and CI
// id so tests can assert on outcomes.
type fixture struct {
	tasks         task.Store
	contracts     contract.Store
	docs          document.Store
	ledger        ledger.Store
	stories       story.Store
	contractDoc   document.Document
	ci            contract.ContractInstance
	reviewTask    task.Task
	evidenceMD    string
	rubricBody    string
	commitRecord  *recordingCommit
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

	evidence, err := led.Append(ctx, ledger.LedgerEntry{
		WorkspaceID: "wksp_a",
		ProjectID:   "proj_a",
		ContractID:  ledger.StringPtr(ci.ID),
		Type:        ledger.TypeEvidence,
		Tags:        []string{"kind:evidence"},
		Content:     "developer evidence body",
	}, now)
	require.NoError(t, err)

	closeRow, err := led.Append(ctx, ledger.LedgerEntry{
		WorkspaceID: "wksp_a",
		ProjectID:   "proj_a",
		ContractID:  ledger.StringPtr(ci.ID),
		Type:        ledger.TypeCloseRequest,
		Tags:        []string{"kind:close-request"},
		Content:     "developer close request",
	}, now)
	require.NoError(t, err)

	payload, _ := json.Marshal(map[string]any{
		"contract_instance_id": ci.ID,
		"contract_name":        ci.ContractName,
		"story_id":             ci.StoryID,
		"close_ledger_id":      closeRow.ID,
		"evidence_ledger_id":   evidence.ID,
	})
	tk, err := tasks.Enqueue(ctx, task.Task{
		WorkspaceID:        "wksp_a",
		ProjectID:          "proj_a",
		ContractInstanceID: ci.ID,
		RequiredRole:       "reviewer",
		Origin:             task.OriginStoryStage,
		Priority:           task.PriorityMedium,
		Payload:            payload,
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
		reviewTask:    tk,
		evidenceMD:    evidence.Content,
		rubricBody:    rubric.Body,
		commitRecord:  &recordingCommit{},
		reviewer:      &stubReviewer{verdict: verdict, err: reviewerErr},
		reviewerAgent: rubric,
		storyDoc:      storyDoc,
	}
}

func (f *fixture) newService(t *testing.T) *service.Service {
	t.Helper()
	svc, err := service.New(service.Config{PollInterval: 5 * time.Millisecond}, service.Deps{
		Tasks:     f.tasks,
		Contracts: f.contracts,
		Docs:      f.docs,
		Ledger:    f.ledger,
		Reviewer:  f.reviewer,
		Commit:    f.commitRecord.fn(),
	})
	require.NoError(t, err)
	return svc
}

func TestService_Tick_HappyPath_AcceptedCommit(t *testing.T) {
	t.Parallel()
	f := newFixture(t, reviewer.Verdict{Outcome: reviewer.VerdictAccepted, Rationale: "ok"}, nil)
	svc := f.newService(t)

	require.True(t, svc.Tick(context.Background()), "expected the service to claim and process the review task")

	assert.Equal(t, int32(1), f.reviewer.calls.Load(), "reviewer should be invoked exactly once")
	call := f.commitRecord.lastCall(t)
	assert.Equal(t, f.ci.ID, call.CIID)
	assert.Equal(t, reviewer.VerdictAccepted, call.Verdict)
	assert.Equal(t, "ok", call.Rationale)
	assert.Equal(t, f.reviewTask.ID, call.TaskID)
	assert.Equal(t, service.ServiceUserID, call.Actor)

	// Reviewer request should carry the rubric + evidence assembled
	// from the contract doc, the system reviewer agent body, and the
	// evidence ledger row.
	// The stub doesn't capture the request shape, but the call count
	// + commit verdict together prove the path executed end-to-end.

	// Task should be claimed (the service hasn't called Close — that's
	// the commit fn's job, which the recording stub no-ops on).
	claimed, err := f.tasks.GetByID(context.Background(), f.reviewTask.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, task.StatusClaimed, claimed.Status)
	assert.Equal(t, service.WorkerID, claimed.ClaimedBy)
}

func TestService_Tick_RejectedCommit(t *testing.T) {
	t.Parallel()
	f := newFixture(t, reviewer.Verdict{Outcome: reviewer.VerdictRejected, Rationale: "missing evidence"}, nil)
	svc := f.newService(t)

	require.True(t, svc.Tick(context.Background()))
	call := f.commitRecord.lastCall(t)
	assert.Equal(t, reviewer.VerdictRejected, call.Verdict)
	assert.Contains(t, call.Rationale, "missing evidence")
}

func TestService_Tick_NeedsMore_TreatedAsRejected(t *testing.T) {
	t.Parallel()
	f := newFixture(t, reviewer.Verdict{
		Outcome:         reviewer.VerdictNeedsMore,
		Rationale:       "incomplete",
		ReviewQuestions: []string{"q1", "q2"},
	}, nil)
	svc := f.newService(t)

	require.True(t, svc.Tick(context.Background()))
	call := f.commitRecord.lastCall(t)
	assert.Equal(t, reviewer.VerdictRejected, call.Verdict, "needs_more must be coerced to rejected for the task path (no contract_respond loop in the queue model)")
	assert.Contains(t, call.Rationale, "q1")
	assert.Contains(t, call.Rationale, "q2")
}

func TestService_Tick_GeminiError_RejectsWithRationale(t *testing.T) {
	t.Parallel()
	f := newFixture(t, reviewer.Verdict{}, errors.New("gemini api timeout"))
	svc := f.newService(t)

	require.True(t, svc.Tick(context.Background()))
	call := f.commitRecord.lastCall(t)
	assert.Equal(t, reviewer.VerdictRejected, call.Verdict, "reviewer error must reject the CI so it doesn't sit pending_review")
	assert.Contains(t, call.Rationale, "reviewer error")
	assert.Contains(t, call.Rationale, "gemini api timeout")
}

func TestService_Tick_NoTaskAvailable_BackoffSignal(t *testing.T) {
	t.Parallel()
	docs := document.NewMemoryStore()
	tasks := task.NewMemoryStore()
	led := ledger.NewMemoryStore()
	stories := story.NewMemoryStore(led)
	contracts := contract.NewMemoryStore(docs, stories)
	commit := &recordingCommit{}
	rev := &stubReviewer{verdict: reviewer.Verdict{Outcome: reviewer.VerdictAccepted}}

	svc, err := service.New(service.Config{PollInterval: 5 * time.Millisecond}, service.Deps{
		Tasks:     tasks,
		Contracts: contracts,
		Docs:      docs,
		Ledger:    led,
		Reviewer:  rev,
		Commit:    commit.fn(),
	})
	require.NoError(t, err)

	assert.False(t, svc.Tick(context.Background()), "empty queue → tick reports no work done")
	assert.Equal(t, int32(0), rev.calls.Load(), "reviewer should not be invoked on empty queue")
}

func TestService_Tick_SkipsNonReviewerTasks(t *testing.T) {
	t.Parallel()
	f := newFixture(t, reviewer.Verdict{Outcome: reviewer.VerdictAccepted}, nil)
	ctx := context.Background()
	now := time.Now().UTC()
	// Inject a separate task without required_role=reviewer; the
	// service must not pick it up.
	other, err := f.tasks.Enqueue(ctx, task.Task{
		WorkspaceID:        "wksp_a",
		ProjectID:          "proj_a",
		ContractInstanceID: f.ci.ID,
		RequiredRole:       "developer",
		Origin:             task.OriginStoryStage,
		Priority:           task.PriorityCritical,
	}, now)
	require.NoError(t, err)

	svc := f.newService(t)
	require.True(t, svc.Tick(ctx))

	// Reviewer task got claimed, developer task is still enqueued.
	developerTask, err := f.tasks.GetByID(ctx, other.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, task.StatusEnqueued, developerTask.Status)
	reviewerTask, err := f.tasks.GetByID(ctx, f.reviewTask.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, task.StatusClaimed, reviewerTask.Status)
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
	commit := &recordingCommit{}

	cases := []struct {
		name string
		deps service.Deps
	}{
		{"missing tasks", service.Deps{Contracts: contracts, Docs: docs, Ledger: led, Reviewer: rev, Commit: commit.fn()}},
		{"missing contracts", service.Deps{Tasks: tasks, Docs: docs, Ledger: led, Reviewer: rev, Commit: commit.fn()}},
		{"missing docs", service.Deps{Tasks: tasks, Contracts: contracts, Ledger: led, Reviewer: rev, Commit: commit.fn()}},
		{"missing ledger", service.Deps{Tasks: tasks, Contracts: contracts, Docs: docs, Reviewer: rev, Commit: commit.fn()}},
		{"missing reviewer", service.Deps{Tasks: tasks, Contracts: contracts, Docs: docs, Ledger: led, Commit: commit.fn()}},
		{"missing commit", service.Deps{Tasks: tasks, Contracts: contracts, Docs: docs, Ledger: led, Reviewer: rev}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := service.New(service.Config{}, tc.deps)
			require.Error(t, err)
		})
	}
}
