package task

import (
	"context"
	"time"
)

// MigrateEnqueuedToPublished walks every workspace and re-stamps tasks
// at the legacy status=enqueued to status=published. Idempotent — a
// repeat run finds zero rows. Returns the count migrated.
//
// sty_c1200f75: the substrate now distinguishes planned (agent-local)
// from published (queue-visible). Pre-c1200f75 rows lived at status=
// enqueued and were always queue-visible; this migration brings them
// into the new state taxonomy.
func MigrateEnqueuedToPublished(ctx context.Context, store Store, now time.Time) (int, error) {
	if store == nil {
		return 0, nil
	}
	rows, err := store.List(ctx, ListOptions{
		Status: StatusEnqueued,
		Limit:  10000,
	}, nil)
	if err != nil {
		return 0, err
	}
	migrated := 0
	for _, t := range rows {
		t.Status = StatusPublished
		if err := store.Save(ctx, t, now); err != nil {
			continue
		}
		migrated++
	}
	return migrated, nil
}
