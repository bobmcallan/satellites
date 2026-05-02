package storystatus

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/story"
)

// fixture wires in-memory ledger/story/contract/doc stores and a
// Reconciler bound to them. Tests share this shape via newFixture.
type fixture struct {
	ctx           context.Context
	now           time.Time
	wsID          string
	projID        string
	docs          document.Store
	led           ledger.Store
	stories       *story.MemoryStore
	contracts     *contract.MemoryStore
	rec           *Reconciler
	contractDocID string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	led := ledger.NewMemoryStore()
	stories := story.NewMemoryStore(led)
	docs := document.NewMemoryStore()
	contracts := contract.NewMemoryStore(docs, stories)

	// Seed a system-scope contract document so contract.Create's
	// validateContractBinding succeeds. Tests don't rely on any
	// particular contract behaviour — they exercise the reconciler.
	cdoc, err := docs.Create(ctx, document.Document{
		Type:   document.TypeContract,
		Scope:  document.ScopeSystem,
		Status: document.StatusActive,
		Name:   "test_contract",
	}, now)
	require.NoError(t, err)

	rec := New(stories, contracts, nil)

	return &fixture{
		ctx:           ctx,
		now:           now,
		wsID:          "wksp_A",
		projID:        "proj_1",
		docs:          docs,
		led:           led,
		stories:       stories,
		contracts:     contracts,
		rec:           rec,
		contractDocID: cdoc.ID,
	}
}

func (f *fixture) seedStory(t *testing.T, status string) story.Story {
	t.Helper()
	s, err := f.stories.Create(f.ctx, story.Story{
		WorkspaceID: f.wsID,
		ProjectID:   f.projID,
		Title:       "fixture",
		Status:      status,
	}, f.now)
	require.NoError(t, err)
	return s
}

func (f *fixture) seedCI(t *testing.T, storyID, status string, sequence int, requiredForClose bool) contract.ContractInstance {
	t.Helper()
	ci, err := f.contracts.Create(f.ctx, contract.ContractInstance{
		StoryID:          storyID,
		ContractID:       f.contractDocID,
		ContractName:     "test_contract",
		Sequence:         sequence,
		Status:           status,
		RequiredForClose: requiredForClose,
	}, f.now)
	require.NoError(t, err)
	return ci
}

// TestReconciler_Claim_ReadyToInProgress asserts that a CI flipping to
// claimed drives the parent story from ready to in_progress.
func TestReconciler_Claim_ReadyToInProgress(t *testing.T) {
	f := newFixture(t)
	st := f.seedStory(t, story.StatusBacklog)
	// Walk the story to ready so the in_progress transition is legal
	// under the existing transition table (sty_dc121948 will allow
	// derived jumps directly from backlog).
	_, err := f.stories.UpdateStatus(f.ctx, st.ID, story.StatusReady, "test", f.now, nil)
	require.NoError(t, err)
	f.seedCI(t, st.ID, contract.StatusClaimed, 0, true)

	require.NoError(t, f.rec.Reconcile(f.ctx, st.ID))

	updated, err := f.stories.GetByID(f.ctx, st.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, story.StatusInProgress, updated.Status)
}

// TestReconciler_AllPassed_FlipsToDone asserts derivation flips a story
// to done once every required CI is in passed/skipped.
func TestReconciler_AllPassed_FlipsToDone(t *testing.T) {
	f := newFixture(t)
	st := f.seedStory(t, story.StatusBacklog)
	_, err := f.stories.UpdateStatus(f.ctx, st.ID, story.StatusReady, "test", f.now, nil)
	require.NoError(t, err)
	_, err = f.stories.UpdateStatus(f.ctx, st.ID, story.StatusInProgress, "test", f.now, nil)
	require.NoError(t, err)
	f.seedCI(t, st.ID, contract.StatusPassed, 0, true)
	f.seedCI(t, st.ID, contract.StatusPassed, 1, true)

	require.NoError(t, f.rec.Reconcile(f.ctx, st.ID))

	updated, err := f.stories.GetByID(f.ctx, st.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, story.StatusDone, updated.Status)
}

// TestReconciler_SkippedAndPassed_FlipsToDone asserts a skipped CI
// counts as terminal alongside passed.
func TestReconciler_SkippedAndPassed_FlipsToDone(t *testing.T) {
	f := newFixture(t)
	st := f.seedStory(t, story.StatusBacklog)
	_, err := f.stories.UpdateStatus(f.ctx, st.ID, story.StatusReady, "test", f.now, nil)
	require.NoError(t, err)
	_, err = f.stories.UpdateStatus(f.ctx, st.ID, story.StatusInProgress, "test", f.now, nil)
	require.NoError(t, err)
	f.seedCI(t, st.ID, contract.StatusSkipped, 0, true)
	f.seedCI(t, st.ID, contract.StatusPassed, 1, true)

	require.NoError(t, f.rec.Reconcile(f.ctx, st.ID))

	updated, err := f.stories.GetByID(f.ctx, st.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, story.StatusDone, updated.Status)
}

// TestReconciler_DuplicateEvent_NoOp asserts re-reconciling a story
// whose status already matches the derivation does not re-emit a
// status update (idempotence under repeated bus events).
func TestReconciler_DuplicateEvent_NoOp(t *testing.T) {
	f := newFixture(t)
	st := f.seedStory(t, story.StatusBacklog)
	_, err := f.stories.UpdateStatus(f.ctx, st.ID, story.StatusReady, "test", f.now, nil)
	require.NoError(t, err)
	f.seedCI(t, st.ID, contract.StatusClaimed, 0, true)

	// First reconcile flips it to in_progress.
	require.NoError(t, f.rec.Reconcile(f.ctx, st.ID))
	first, err := f.stories.GetByID(f.ctx, st.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, story.StatusInProgress, first.Status)
	priorUpdated := first.UpdatedAt

	// Second reconcile is a no-op — UpdatedAt should not advance.
	require.NoError(t, f.rec.Reconcile(f.ctx, st.ID))
	second, err := f.stories.GetByID(f.ctx, st.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, story.StatusInProgress, second.Status)
	assert.Equal(t, priorUpdated, second.UpdatedAt, "no-op reconcile must not bump UpdatedAt")
}

// TestReconciler_Failed_KeepsInProgress asserts a failed CI alongside
// passed CIs keeps the story at in_progress (not done).
func TestReconciler_Failed_KeepsInProgress(t *testing.T) {
	f := newFixture(t)
	st := f.seedStory(t, story.StatusBacklog)
	_, err := f.stories.UpdateStatus(f.ctx, st.ID, story.StatusReady, "test", f.now, nil)
	require.NoError(t, err)
	_, err = f.stories.UpdateStatus(f.ctx, st.ID, story.StatusInProgress, "test", f.now, nil)
	require.NoError(t, err)
	f.seedCI(t, st.ID, contract.StatusPassed, 0, true)
	f.seedCI(t, st.ID, contract.StatusFailed, 1, true)

	require.NoError(t, f.rec.Reconcile(f.ctx, st.ID))

	updated, err := f.stories.GetByID(f.ctx, st.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, story.StatusInProgress, updated.Status,
		"failed CI must keep story in_progress, not done")
}

// TestReconciler_Backfill_RepairsDriftedStory asserts Backfill walks
// the story slice and advances stories whose current status is stale
// relative to derivation.
func TestReconciler_Backfill_RepairsDriftedStory(t *testing.T) {
	f := newFixture(t)
	stale := f.seedStory(t, story.StatusBacklog)
	// Walk to ready so claimed-CI derivation is legal.
	_, err := f.stories.UpdateStatus(f.ctx, stale.ID, story.StatusReady, "test", f.now, nil)
	require.NoError(t, err)
	f.seedCI(t, stale.ID, contract.StatusClaimed, 0, true)

	clean := f.seedStory(t, story.StatusBacklog)
	_, err = f.stories.UpdateStatus(f.ctx, clean.ID, story.StatusReady, "test", f.now, nil)
	require.NoError(t, err)

	stories, err := f.stories.List(f.ctx, f.projID, story.ListOptions{}, nil)
	require.NoError(t, err)

	touched, errored := f.rec.Backfill(f.ctx, stories)
	assert.Equal(t, 1, touched)
	assert.Equal(t, 0, errored)

	gotStale, err := f.stories.GetByID(f.ctx, stale.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, story.StatusInProgress, gotStale.Status)

	gotClean, err := f.stories.GetByID(f.ctx, clean.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, story.StatusReady, gotClean.Status,
		"a story whose derivation matches its current status must not be touched")
}

// TestReconciler_AsLedgerListener asserts the reconciler is a
// well-formed ledger.Listener and fires when wired via AddListener.
// Spy listener captures the entry the reconciler observes.
func TestReconciler_AsLedgerListener(t *testing.T) {
	f := newFixture(t)
	st := f.seedStory(t, story.StatusBacklog)
	_, err := f.stories.UpdateStatus(f.ctx, st.ID, story.StatusReady, "test", f.now, nil)
	require.NoError(t, err)
	f.seedCI(t, st.ID, contract.StatusClaimed, 0, true)

	mem := f.led.(*ledger.MemoryStore)
	mem.AddListener(f.rec)

	// Append a kind:action_claim row tagged with the story id.
	storyIDPtr := st.ID
	_, err = f.led.Append(f.ctx, ledger.LedgerEntry{
		WorkspaceID: f.wsID,
		ProjectID:   f.projID,
		StoryID:     &storyIDPtr,
		Type:        ledger.TypeActionClaim,
		Tags:        []string{"kind:action_claim"},
		Content:     "test claim",
		CreatedBy:   "u_test",
	}, f.now)
	require.NoError(t, err)

	// The reconciler is synchronous — by the time Append returns, the
	// story status has been advanced.
	updated, err := f.stories.GetByID(f.ctx, st.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, story.StatusInProgress, updated.Status)
}

// TestReconciler_NoStoryIDOnEntry_NoOp asserts a ledger row without a
// story_id is dropped silently (KV writes, system events, etc.).
func TestReconciler_NoStoryIDOnEntry_NoOp(t *testing.T) {
	f := newFixture(t)

	mem := f.led.(*ledger.MemoryStore)
	mem.AddListener(f.rec)

	_, err := f.led.Append(f.ctx, ledger.LedgerEntry{
		WorkspaceID: f.wsID,
		ProjectID:   f.projID,
		Type:        ledger.TypeKV,
		Tags:        []string{"kind:action_claim"},
		Content:     "no story_id here",
		CreatedBy:   "u_test",
	}, f.now)
	require.NoError(t, err)
	// Nothing to assert beyond no-panic — coverage for the early return.
}

// TestReconciler_NonTriggerKind_NoOp asserts ledger rows whose tags do
// not carry a CI-state-transition kind do not invoke Reconcile.
func TestReconciler_NonTriggerKind_NoOp(t *testing.T) {
	f := newFixture(t)
	st := f.seedStory(t, story.StatusBacklog)
	storyIDPtr := st.ID

	mem := f.led.(*ledger.MemoryStore)
	mem.AddListener(f.rec)

	_, err := f.led.Append(f.ctx, ledger.LedgerEntry{
		WorkspaceID: f.wsID,
		ProjectID:   f.projID,
		StoryID:     &storyIDPtr,
		Type:        ledger.TypeEvidence,
		Tags:        []string{"kind:evidence"}, // not a trigger kind
		Content:     "evidence row",
		CreatedBy:   "u_test",
	}, f.now)
	require.NoError(t, err)

	got, err := f.stories.GetByID(f.ctx, st.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, story.StatusBacklog, got.Status,
		"non-trigger kind must not advance story status")
}

// TestReconciler_BacklogToInProgressDirect (sty_dc121948) asserts the
// reconciler now writes via UpdateStatusDerived and can advance a
// story straight from backlog to in_progress without the manual
// "walk to ready first" choreography the prior transition guard
// required.
func TestReconciler_BacklogToInProgressDirect(t *testing.T) {
	f := newFixture(t)
	st := f.seedStory(t, story.StatusBacklog)
	// No manual walk-to-ready — derivation must succeed directly.
	f.seedCI(t, st.ID, contract.StatusClaimed, 0, true)

	require.NoError(t, f.rec.Reconcile(f.ctx, st.ID))

	updated, err := f.stories.GetByID(f.ctx, st.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, story.StatusInProgress, updated.Status,
		"reconciler must flip backlog → in_progress directly via UpdateStatusDerived")
}

func TestKindOf(t *testing.T) {
	assert.Equal(t, "action_claim", kindOf([]string{"kind:action_claim", "story:sty_x"}))
	assert.Equal(t, "", kindOf([]string{"story:sty_x"}))
	assert.Equal(t, "", kindOf(nil))
}
