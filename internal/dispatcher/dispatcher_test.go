package dispatcher_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobmcallan/satellites/internal/dispatcher"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/task"
)

func TestDispatcher_NoTasks_ReturnsErrNoTaskAvailable(t *testing.T) {
	t.Parallel()
	d := dispatcher.New(task.NewMemoryStore(), ledger.NewMemoryStore(), nil, dispatcher.Options{})
	_, err := d.Claim(context.Background(), "worker", []string{"w"}, time.Now())
	assert.ErrorIs(t, err, task.ErrNoTaskAvailable)
}

func TestDispatcher_ClaimPriorityPrecedence(t *testing.T) {
	t.Parallel()
	store := task.NewMemoryStore()
	d := dispatcher.New(store, ledger.NewMemoryStore(), nil, dispatcher.Options{})
	now := time.Now().UTC()
	// Medium first (older), critical later.
	_, _ = store.Enqueue(context.Background(), task.Task{
		WorkspaceID: "w",
		Origin:      task.OriginScheduled,
		Priority:    task.PriorityMedium,
	}, now)
	crit, _ := store.Enqueue(context.Background(), task.Task{
		WorkspaceID: "w",
		Origin:      task.OriginScheduled,
		Priority:    task.PriorityCritical,
	}, now.Add(time.Second))

	picked, err := d.Claim(context.Background(), "worker", []string{"w"}, now.Add(2*time.Second))
	require.NoError(t, err)
	assert.Equal(t, crit.ID, picked.ID, "critical should claim before medium even when medium was enqueued earlier")
}

func TestDispatcher_WatchdogReclaim_FiresAfterExpectedDuration(t *testing.T) {
	t.Parallel()
	store := task.NewMemoryStore()
	ldgr := ledger.NewMemoryStore()
	d := dispatcher.New(store, ldgr, nil, dispatcher.Options{ReclaimMultiplier: 2.0})
	now := time.Now().UTC()
	enq, err := store.Enqueue(context.Background(), task.Task{
		WorkspaceID:      "w",
		Origin:           task.OriginScheduled,
		Priority:         task.PriorityMedium,
		ExpectedDuration: time.Minute,
	}, now)
	require.NoError(t, err)
	// Claim it.
	claimed, err := store.Claim(context.Background(), "worker", []string{"w"}, now.Add(time.Second))
	require.NoError(t, err)
	assert.Equal(t, enq.ID, claimed.ID)

	// Forward 3 minutes: past 2× expected_duration (2 min budget).
	future := now.Add(3 * time.Minute)
	reclaimed, err := d.ReclaimExpired(context.Background(), future)
	require.NoError(t, err)
	assert.Equal(t, 1, reclaimed)

	// Task is back in enqueued; reclaim_count incremented; claimed_by cleared.
	after, err := store.GetByID(context.Background(), enq.ID, []string{"w"})
	require.NoError(t, err)
	assert.Equal(t, task.StatusEnqueued, after.Status)
	assert.Equal(t, 1, after.ReclaimCount)
	assert.Empty(t, after.ClaimedBy)

	// Ledger row written.
	rows, err := ldgr.List(context.Background(), "", ledger.ListOptions{}, nil)
	require.NoError(t, err)
	found := false
	for _, r := range rows {
		for _, tag := range r.Tags {
			if tag == "kind:task-reclaimed" {
				found = true
			}
		}
	}
	assert.True(t, found, "kind:task-reclaimed ledger row should be written")
}

func TestDispatcher_WatchdogReclaim_SkipsWithinBudget(t *testing.T) {
	t.Parallel()
	store := task.NewMemoryStore()
	d := dispatcher.New(store, ledger.NewMemoryStore(), nil, dispatcher.Options{ReclaimMultiplier: 2.0})
	now := time.Now().UTC()
	_, _ = store.Enqueue(context.Background(), task.Task{
		WorkspaceID:      "w",
		Origin:           task.OriginScheduled,
		Priority:         task.PriorityMedium,
		ExpectedDuration: time.Minute,
	}, now)
	_, err := store.Claim(context.Background(), "worker", []string{"w"}, now.Add(time.Second))
	require.NoError(t, err)

	// Only 30 seconds past claim — well within 2× 1-minute budget.
	reclaimed, err := d.ReclaimExpired(context.Background(), now.Add(30*time.Second))
	require.NoError(t, err)
	assert.Equal(t, 0, reclaimed)
}

func TestDispatcher_WatchdogReclaim_SkipsTasksWithoutExpectedDuration(t *testing.T) {
	t.Parallel()
	store := task.NewMemoryStore()
	d := dispatcher.New(store, ledger.NewMemoryStore(), nil, dispatcher.Options{ReclaimMultiplier: 2.0})
	now := time.Now().UTC()
	_, _ = store.Enqueue(context.Background(), task.Task{
		WorkspaceID: "w",
		Origin:      task.OriginScheduled,
		Priority:    task.PriorityMedium,
	}, now)
	_, err := store.Claim(context.Background(), "worker", []string{"w"}, now.Add(time.Second))
	require.NoError(t, err)

	// Forward an hour; ExpectedDuration=0 so no budget → skipped.
	reclaimed, err := d.ReclaimExpired(context.Background(), now.Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 0, reclaimed, "tasks without expected_duration have no budget to expire against")
}

func TestDispatcher_StartStop_Idempotent(t *testing.T) {
	t.Parallel()
	d := dispatcher.New(task.NewMemoryStore(), ledger.NewMemoryStore(), nil, dispatcher.Options{
		PollInterval: 10 * time.Millisecond,
	})
	ctx := context.Background()
	require.NoError(t, d.Start(ctx))
	err := d.Start(ctx)
	assert.Error(t, err, "starting twice should error")
	d.Stop()
	d.Stop() // idempotent
	// Restart should work.
	require.NoError(t, d.Start(ctx))
	d.Stop()
}
