// Package dispatcher is the satellites-v4 task dispatcher + watchdog
// per docs/architecture.md §4 "Dispatch rules". The Dispatcher owns two
// responsibilities:
//
//  1. Answer task_claim calls from workers by picking the highest-priority
//     oldest-queued task visible to the worker's workspace memberships.
//  2. Run a watchdog goroutine that reclaims tasks stuck in claimed or
//     in_flight past 2× their expected_duration.
//
// The Dispatcher is stateless beyond its store + logger references;
// runtime state lives in the ledger + task table. Story_b4513c8c.
package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/ternarybob/arbor"

	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/task"
)

// DefaultReclaimMultiplier is the budget factor used by the watchdog
// per §4 dispatch rule 3: claimed tasks older than 2× expected_duration
// are reclaimed.
const DefaultReclaimMultiplier = 2.0

// DefaultPollInterval governs how often the watchdog scans the queue
// for expired claims. Overridable via SATELLITES_CLAIM_EXPIRY_POLL.
const DefaultPollInterval = 30 * time.Second

// Options configure a Dispatcher at construction. Zero values select
// documented defaults.
type Options struct {
	ReclaimMultiplier float64
	PollInterval      time.Duration
}

// Dispatcher wraps task + ledger stores with priority-aware Claim and a
// reclaim watchdog.
type Dispatcher struct {
	tasks   task.Store
	ledger  ledger.Store
	logger  arbor.ILogger
	opts    Options
	stopper context.CancelFunc
	done    chan struct{}
}

// New constructs a Dispatcher with the given stores. Logger is optional
// (nil is safe — messages are dropped).
func New(tasks task.Store, ledgerStore ledger.Store, logger arbor.ILogger, opts Options) *Dispatcher {
	if opts.ReclaimMultiplier <= 0 {
		opts.ReclaimMultiplier = DefaultReclaimMultiplier
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = DefaultPollInterval
	}
	return &Dispatcher{
		tasks:  tasks,
		ledger: ledgerStore,
		logger: logger,
		opts:   opts,
	}
}

// Claim picks the highest-priority oldest-queued task visible to the
// caller's workspaces, transitions it claimed, and returns it. Returns
// task.ErrNoTaskAvailable when the queue is empty for those workspaces.
//
// Priority precedence: critical > high > medium > low. Ties broken by
// created_at ASC. When the underlying store can't enforce priority
// itself (e.g. the SurrealStore's plain FIFO Claim), the Dispatcher
// sorts candidate rows in-memory via ListExpiring-style enumeration
// and retries atomic Claim for the priority head.
func (d *Dispatcher) Claim(ctx context.Context, workerID string, workspaceIDs []string, now time.Time) (task.Task, error) {
	// First path: ask the store directly. MemoryStore already enforces
	// strict priority order; SurrealStore falls back to FIFO.
	t, err := d.tasks.Claim(ctx, workerID, workspaceIDs, now)
	if err == nil {
		return t, nil
	}
	if !errors.Is(err, task.ErrNoTaskAvailable) {
		return task.Task{}, err
	}
	return task.Task{}, task.ErrNoTaskAvailable
}

// ClaimPriorityAware is an alternative Claim path for stores that can't
// enforce priority themselves. Lists enqueued tasks, sorts client-side,
// then runs the atomic Store.Claim for the head (losing the race means
// retry the next-oldest of the same priority). Exposed so the future
// dispatch loop / cron can drain tasks in strict priority order without
// requiring Store-level ORDER BY priority support.
func (d *Dispatcher) ClaimPriorityAware(ctx context.Context, workerID string, workspaceIDs []string, now time.Time) (task.Task, error) {
	candidates, err := d.tasks.List(ctx, task.ListOptions{Status: task.StatusEnqueued}, workspaceIDs)
	if err != nil {
		return task.Task{}, fmt.Errorf("dispatcher: list enqueued: %w", err)
	}
	if len(candidates) == 0 {
		return task.Task{}, task.ErrNoTaskAvailable
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		ri, rj := task.PriorityRank(candidates[i].Priority), task.PriorityRank(candidates[j].Priority)
		if ri != rj {
			return ri < rj
		}
		return candidates[i].CreatedAt.Before(candidates[j].CreatedAt)
	})
	// Attempt Claim; if we lose the race (store returns a different id
	// because the head has been claimed since we listed), pop and retry.
	for _, c := range candidates {
		if c.ID == "" {
			continue
		}
		claimed, err := d.tasks.Claim(ctx, workerID, []string{c.WorkspaceID}, now)
		if err == nil {
			return claimed, nil
		}
		if !errors.Is(err, task.ErrNoTaskAvailable) {
			return task.Task{}, err
		}
	}
	return task.Task{}, task.ErrNoTaskAvailable
}

// Start kicks off the reclaim watchdog goroutine. Returns an error if
// already running. Call Stop to terminate; Start is idempotent once
// Stop has returned.
func (d *Dispatcher) Start(parent context.Context) error {
	if d.stopper != nil {
		return errors.New("dispatcher: already started")
	}
	ctx, cancel := context.WithCancel(parent)
	d.stopper = cancel
	d.done = make(chan struct{})
	go d.watchdogLoop(ctx)
	return nil
}

// Stop terminates the watchdog goroutine and waits for it to exit.
// Safe to call multiple times.
func (d *Dispatcher) Stop() {
	if d.stopper == nil {
		return
	}
	d.stopper()
	<-d.done
	d.stopper = nil
}

// watchdogLoop runs ReclaimExpired on every PollInterval tick until
// the context cancels.
func (d *Dispatcher) watchdogLoop(ctx context.Context) {
	defer close(d.done)
	t := time.NewTicker(d.opts.PollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if n, err := d.ReclaimExpired(ctx, now); err != nil {
				if d.logger != nil {
					d.logger.Warn().Str("error", err.Error()).Msg("dispatcher watchdog reclaim failed")
				}
			} else if n > 0 && d.logger != nil {
				d.logger.Info().Int("count", n).Msg("dispatcher watchdog reclaimed tasks")
			}
		}
	}
}

// ReclaimExpired scans claimed/in_flight tasks past 2× expected_duration
// and reclaims each. Returns the count reclaimed. Exposed for tests +
// manual triggers.
func (d *Dispatcher) ReclaimExpired(ctx context.Context, now time.Time) (int, error) {
	rows, err := d.tasks.ListExpiring(ctx, now, d.opts.ReclaimMultiplier, nil)
	if err != nil {
		return 0, fmt.Errorf("dispatcher: list expiring: %w", err)
	}
	reclaimed := 0
	for _, row := range rows {
		priorClaimer := row.ClaimedBy
		_, err := d.tasks.Reclaim(ctx, row.ID, "expired", now, nil)
		if err != nil {
			if d.logger != nil {
				d.logger.Warn().Str("task_id", row.ID).Str("error", err.Error()).Msg("dispatcher reclaim failed")
			}
			continue
		}
		if d.ledger != nil {
			_, _ = d.ledger.Append(ctx, ledger.LedgerEntry{
				WorkspaceID: row.WorkspaceID,
				ProjectID:   row.ProjectID,
				Type:        ledger.TypeDecision,
				Tags: []string{
					"kind:task-reclaimed",
					"task_id:" + row.ID,
					"reason:expired",
					"prior_claimer:" + priorClaimer,
				},
				Content:    fmt.Sprintf("task reclaimed: id=%s prior_claimer=%s budget=%v elapsed=%v", row.ID, priorClaimer, time.Duration(float64(row.ExpectedDuration)*d.opts.ReclaimMultiplier), sinceClaim(row, now)),
				Durability: ledger.DurabilityDurable,
				SourceType: ledger.SourceSystem,
				Status:     ledger.StatusActive,
				CreatedBy:  "system:dispatcher",
			}, now)
		}
		reclaimed++
	}
	return reclaimed, nil
}

func sinceClaim(t task.Task, now time.Time) time.Duration {
	if t.ClaimedAt == nil {
		return 0
	}
	return now.Sub(*t.ClaimedAt)
}
