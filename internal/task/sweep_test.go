package task_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobmcallan/satellites/internal/task"
)

// TestSweep_ArchivesOnlyClosedRowsOlderThanCutoff verifies the
// sty_dc2998c5 boundary: closed rows with completed_at <= cutoff move
// to archived; closed rows newer than cutoff stay closed; non-closed
// rows are unaffected.
func TestSweep_ArchivesOnlyClosedRowsOlderThanCutoff(t *testing.T) {
	t.Parallel()
	store := task.NewMemoryStore()
	ctx := context.Background()
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-90 * 24 * time.Hour)

	// Aged closed task (just past cutoff) — should archive.
	old, err := store.Enqueue(ctx, task.Task{
		WorkspaceID: "w", Origin: task.OriginScheduled, Priority: task.PriorityMedium,
	}, now.Add(-100*24*time.Hour))
	require.NoError(t, err)
	if _, err := store.ClaimByID(ctx, old.ID, "worker", now.Add(-100*24*time.Hour), nil); err != nil {
		t.Fatalf("claim old: %v", err)
	}
	if _, err := store.Close(ctx, old.ID, task.OutcomeSuccess, now.Add(-100*24*time.Hour), nil); err != nil {
		t.Fatalf("close old: %v", err)
	}

	// Young closed task (within window) — should stay closed.
	young, err := store.Enqueue(ctx, task.Task{
		WorkspaceID: "w", Origin: task.OriginScheduled, Priority: task.PriorityMedium,
	}, now.Add(-7*24*time.Hour))
	require.NoError(t, err)
	if _, err := store.ClaimByID(ctx, young.ID, "worker", now.Add(-7*24*time.Hour), nil); err != nil {
		t.Fatalf("claim young: %v", err)
	}
	if _, err := store.Close(ctx, young.ID, task.OutcomeSuccess, now.Add(-7*24*time.Hour), nil); err != nil {
		t.Fatalf("close young: %v", err)
	}

	// Enqueued task — never archived regardless of age.
	enq, err := store.Enqueue(ctx, task.Task{
		WorkspaceID: "w", Origin: task.OriginScheduled, Priority: task.PriorityMedium,
	}, now.Add(-200*24*time.Hour))
	require.NoError(t, err)

	res, err := task.Sweep(ctx, store, cutoff, now, []string{"w"})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Archived, "exactly one row past cutoff")

	// Old row is now archived; default List skips it.
	defaultRows, err := store.List(ctx, task.ListOptions{}, []string{"w"})
	require.NoError(t, err)
	for _, r := range defaultRows {
		if r.ID == old.ID {
			t.Errorf("default List should hide archived row %q", old.ID)
		}
	}

	// include_archived surfaces it.
	allRows, err := store.List(ctx, task.ListOptions{IncludeArchived: true}, []string{"w"})
	require.NoError(t, err)
	found := false
	for _, r := range allRows {
		if r.ID == old.ID {
			found = true
			assert.Equal(t, task.StatusArchived, r.Status)
		}
	}
	assert.True(t, found, "include_archived must surface archived rows")

	// Young + enqueued rows untouched.
	youngAfter, err := store.GetByID(ctx, young.ID, []string{"w"})
	require.NoError(t, err)
	assert.Equal(t, task.StatusClosed, youngAfter.Status)
	enqAfter, err := store.GetByID(ctx, enq.ID, []string{"w"})
	require.NoError(t, err)
	assert.Equal(t, task.StatusEnqueued, enqAfter.Status)

	// Idempotence: a second pass archives nothing.
	res2, err := task.Sweep(ctx, store, cutoff, now.Add(time.Hour), []string{"w"})
	require.NoError(t, err)
	assert.Equal(t, 0, res2.Archived)
}

// TestArchive_RejectsNonClosedRows confirms only closed → archived is a
// legal transition.
func TestArchive_RejectsNonClosedRows(t *testing.T) {
	t.Parallel()
	store := task.NewMemoryStore()
	ctx := context.Background()
	now := time.Now().UTC()

	enq, err := store.Enqueue(ctx, task.Task{
		WorkspaceID: "w", Origin: task.OriginScheduled, Priority: task.PriorityMedium,
	}, now)
	require.NoError(t, err)
	_, err = store.Archive(ctx, enq.ID, now, []string{"w"})
	assert.ErrorIs(t, err, task.ErrInvalidTransition, "enqueued → archived rejected")
}
