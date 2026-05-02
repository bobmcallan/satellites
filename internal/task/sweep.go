package task

import (
	"context"
	"errors"
	"time"
)

// SweepResult reports what one Sweep pass touched. Errored counts
// per-row archive failures; the sweep continues past individual
// failures so a single bad row doesn't stall the run.
type SweepResult struct {
	Scanned  int
	Archived int
	Errored  int
}

// Sweep flips closed tasks older than retention into archived. The
// caller passes the sweep cutoff (typically now - retention_days);
// rows whose CompletedAt is at or before cutoff are archived. Returns
// a SweepResult describing the pass.
//
// memberships scopes the workspaces the sweep covers. Pass nil for
// system-identity callers (the boot loop in cmd/satellites runs the
// sweep with nil so every workspace's retention is honoured even
// though no human session is attached). sty_dc2998c5.
func Sweep(ctx context.Context, store Store, cutoff time.Time, now time.Time, memberships []string) (SweepResult, error) {
	out := SweepResult{}
	if store == nil {
		return out, errors.New("task: sweep requires a store")
	}
	rows, err := store.List(ctx, ListOptions{Status: StatusClosed, Limit: 500, IncludeArchived: false}, memberships)
	if err != nil {
		return out, err
	}
	out.Scanned = len(rows)
	for _, t := range rows {
		if t.CompletedAt == nil {
			continue
		}
		if t.CompletedAt.After(cutoff) {
			continue
		}
		if _, aerr := store.Archive(ctx, t.ID, now, memberships); aerr != nil {
			out.Errored++
			continue
		}
		out.Archived++
	}
	return out, nil
}
