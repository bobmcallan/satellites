package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ternarybob/arbor"

	"github.com/bobmcallan/satellites/internal/codeindex"
	"github.com/bobmcallan/satellites/internal/hubemit"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/task"
)

// DefaultWorkerPollInterval governs how often the in-process reindex
// worker scans the queue for pending reindex_repo tasks. Overridable
// via WorkerOptions.PollInterval.
const DefaultWorkerPollInterval = 10 * time.Second

// DefaultWorkerID is the worker identity stamped on the claim ledger
// row. Identifies the in-process reindex loop in audit trails.
const DefaultWorkerID = "satellites-reindex-worker"

// WorkerOptions configure a reindex Worker at construction. Zero
// values select documented defaults.
type WorkerOptions struct {
	PollInterval time.Duration
	WorkerID     string
}

// Worker is the in-process consumer of reindex_repo tasks. It polls
// the task store for enqueued tasks tagged with the reindex_repo
// handler, claims them, and runs HandleReindex inline. Lives next to
// the dispatcher in cmd/satellites/main.go so the satellites server
// binary self-contains the repo collection pipeline — operators don't
// need to run a second binary just to make repo_scan complete.
type Worker struct {
	deps    Deps
	logger  arbor.ILogger
	opts    WorkerOptions
	stopper context.CancelFunc
	done    chan struct{}
	wg      sync.WaitGroup
}

// NewWorker constructs an idle Worker. Call Start to begin polling.
func NewWorker(repos Store, tasks task.Store, led ledger.Store, indexer codeindex.Indexer, publisher hubemit.Publisher, logger arbor.ILogger, opts WorkerOptions) *Worker {
	if opts.PollInterval <= 0 {
		opts.PollInterval = DefaultWorkerPollInterval
	}
	if opts.WorkerID == "" {
		opts.WorkerID = DefaultWorkerID
	}
	return &Worker{
		deps: Deps{
			Repos:     repos,
			Tasks:     tasks,
			Ledger:    led,
			Indexer:   indexer,
			Publisher: publisher,
		},
		logger: logger,
		opts:   opts,
	}
}

// Start launches the polling goroutine. Returns an error if the
// worker is already running or required deps are nil.
func (w *Worker) Start(ctx context.Context) error {
	if w.deps.Repos == nil || w.deps.Tasks == nil || w.deps.Ledger == nil || w.deps.Indexer == nil {
		return fmt.Errorf("repo worker: required deps missing")
	}
	if w.stopper != nil {
		return fmt.Errorf("repo worker: already started")
	}
	loopCtx, cancel := context.WithCancel(ctx)
	w.stopper = cancel
	w.done = make(chan struct{})
	w.wg.Add(1)
	go w.loop(loopCtx)
	return nil
}

// Stop signals the polling goroutine to exit and waits for it.
func (w *Worker) Stop() {
	if w.stopper == nil {
		return
	}
	w.stopper()
	w.wg.Wait()
	w.stopper = nil
}

// loop polls the queue at PollInterval, draining any reindex_repo
// tasks visible to the system identity.
func (w *Worker) loop(ctx context.Context) {
	defer w.wg.Done()
	defer close(w.done)
	ticker := time.NewTicker(w.opts.PollInterval)
	defer ticker.Stop()
	w.drain(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.drain(ctx)
		}
	}
}

// drain processes every available reindex_repo task in one pass.
// Stops on context cancel, claim error, or empty queue.
func (w *Worker) drain(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		picked, err := w.claimReindexTask(ctx)
		if err != nil {
			if !errors.Is(err, task.ErrNoTaskAvailable) {
				w.logger.Warn().Str("error", err.Error()).Msg("repo worker: claim failed")
			}
			return
		}
		if picked == nil {
			return
		}
		w.run(ctx, *picked)
	}
}

// claimReindexTask scans the enqueued queue for a task whose payload
// names the reindex_repo handler, then atomically claims it. Returns
// nil when no matching task exists. Tasks belonging to other handlers
// are left in the queue for their owners to claim.
func (w *Worker) claimReindexTask(ctx context.Context) (*task.Task, error) {
	pending, err := w.deps.Tasks.List(ctx, task.ListOptions{
		Status: task.StatusEnqueued,
		Limit:  20,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("list pending: %w", err)
	}
	for _, t := range pending {
		var p ReindexPayload
		if err := json.Unmarshal(t.Payload, &p); err != nil {
			continue
		}
		if p.Handler != ReindexHandlerName {
			continue
		}
		now := time.Now().UTC()
		claimed, err := w.deps.Tasks.Claim(ctx, w.opts.WorkerID, []string{t.WorkspaceID}, now)
		if err != nil {
			if errors.Is(err, task.ErrNoTaskAvailable) {
				continue
			}
			return nil, fmt.Errorf("claim: %w", err)
		}
		// Race guard: confirm we got a reindex_repo task; otherwise the
		// queue moved under us and a different handler-owner will pick
		// it up. Reclaim back to enqueued.
		var claimedPayload ReindexPayload
		if err := json.Unmarshal(claimed.Payload, &claimedPayload); err != nil || claimedPayload.Handler != ReindexHandlerName {
			if _, rerr := w.deps.Tasks.Reclaim(ctx, claimed.ID, "wrong-handler", now, nil); rerr != nil {
				w.logger.Warn().Str("error", rerr.Error()).Str("task_id", claimed.ID).Msg("repo worker: reclaim mismatched task")
			}
			continue
		}
		return &claimed, nil
	}
	return nil, nil
}

// run executes HandleReindex against the claimed task and closes it
// with the matching outcome.
func (w *Worker) run(ctx context.Context, t task.Task) {
	outcome, err := HandleReindex(ctx, w.deps, t)
	if err != nil {
		w.logger.Warn().Str("error", err.Error()).Str("task_id", t.ID).Msg("repo worker: HandleReindex failed")
	}
	now := time.Now().UTC()
	if _, closeErr := w.deps.Tasks.Close(ctx, t.ID, string(outcome), now, nil); closeErr != nil {
		w.logger.Warn().Str("error", closeErr.Error()).Str("task_id", t.ID).Str("outcome", string(outcome)).Msg("repo worker: close failed")
	}
}
