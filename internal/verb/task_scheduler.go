// Scheduled workspace agent-tasks (epic:workspace-agents, sty_7bf60667). The
// TaskScheduler is a per-instance background ticker — modelled on
// internal/embed.Worker — that runs trigger:"schedule" agent-tasks on their
// interval without a human or agent invoking them.
//
// next_run_at and the single-flight running-set live IN MEMORY: a restart
// resets cadence (next run = one interval after the task is next seen). These
// are interval schedules, not wall-clock, so that is acceptable and needs no
// migration. Single-flight is per-instance, like the embed worker.

package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/synth"
)

// scheduledTask is one schedule-triggered task the scheduler discovered.
type scheduledTask struct {
	WorkspaceID string
	Name        string
	Config      taskConfig
}

// TaskScheduler runs scheduled agent-tasks on their interval. Its seams (list,
// run, record, now) are injectable so tests drive it deterministically without
// a database, network, or real clock.
type TaskScheduler struct {
	interval time.Duration // tick granularity
	now      func() time.Time
	list     func(ctx context.Context) ([]scheduledTask, error)
	run      func(ctx context.Context, t scheduledTask) (WorkspaceTaskRunResponse, error)
	record   func(ctx context.Context, t scheduledTask, resp WorkspaceTaskRunResponse, runErr error)

	mu      sync.Mutex
	nextRun map[string]time.Time
	running map[string]bool
}

// NewTaskScheduler wires the production scheduler: it discovers agent-task
// documents across every workspace, runs each due scheduled task through the
// shared run path, and records each run on the ledger. A non-positive tick
// interval falls back to 30s.
func NewTaskScheduler(tickInterval time.Duration, ledgerStore *ledger.Store) *TaskScheduler {
	if tickInterval <= 0 {
		tickInterval = 30 * time.Second
	}
	s := &TaskScheduler{
		interval: tickInterval,
		now:      time.Now,
		list:     defaultScheduledTaskLister,
		run: func(ctx context.Context, t scheduledTask) (WorkspaceTaskRunResponse, error) {
			return runWorkspaceTask(ctx, t.WorkspaceID, t.Config.TaskSkill, t.Config.OutputName, t.Config.Executor, "scheduler")
		},
		record:  defaultRunRecorder(ledgerStore),
		nextRun: map[string]time.Time{},
		running: map[string]bool{},
	}
	return s
}

// Run ticks until ctx is cancelled.
func (s *TaskScheduler) Run(ctx context.Context) {
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tick(ctx)
		}
	}
}

// tick discovers scheduled tasks and launches a single-flight run for each that
// is due. A task seen for the first time is scheduled one interval out (no
// immediate run, so a restart does not re-fire every task); thereafter it runs
// when now ≥ its next_run_at and is not already running.
func (s *TaskScheduler) tick(ctx context.Context) {
	tasks, err := s.list(ctx)
	if err != nil {
		arbor.WarnCtx(ctx, "task scheduler: list", "err", err)
		return
	}
	now := s.now()
	for _, t := range tasks {
		if t.Config.Trigger != triggerSchedule || t.Config.Schedule == nil || t.Config.Schedule.IntervalSeconds <= 0 {
			continue
		}
		key := t.WorkspaceID + "/" + t.Name
		s.mu.Lock()
		if s.running[key] {
			s.mu.Unlock()
			continue // single-flight: a prior run is still in flight
		}
		next, seen := s.nextRun[key]
		if !seen {
			// First sighting: defer the first run one interval out.
			s.nextRun[key] = now.Add(s.intervalOf(t))
			s.mu.Unlock()
			continue
		}
		if now.Before(next) {
			s.mu.Unlock()
			continue // not due yet
		}
		s.running[key] = true
		s.mu.Unlock()
		go s.runOne(ctx, t, key)
	}
}

// runOne executes a single scheduled run, records it, then advances next_run_at
// and releases the single-flight lock — even when the run errors or reports
// not-run, so a failure never wedges the schedule.
func (s *TaskScheduler) runOne(ctx context.Context, t scheduledTask, key string) {
	resp, runErr := s.run(ctx, t)
	if s.record != nil {
		s.record(ctx, t, resp, runErr)
	}
	s.mu.Lock()
	s.nextRun[key] = s.now().Add(s.intervalOf(t))
	delete(s.running, key)
	s.mu.Unlock()
}

func (s *TaskScheduler) intervalOf(t scheduledTask) time.Duration {
	return time.Duration(t.Config.Schedule.IntervalSeconds) * time.Second
}

// defaultScheduledTaskLister discovers agent-task documents across every
// workspace (no workspace_id filter) and returns those whose trigger is
// "schedule". The config body is read per task (List does not carry bodies).
func defaultScheduledTaskLister(ctx context.Context) ([]scheduledTask, error) {
	if documentStore == nil {
		return nil, nil
	}
	res, err := documentStore.List(ctx, document.ListFilter{
		Type:       document.TypeDocument,
		Scope:      document.ScopeWorkspace,
		NamePrefix: synth.AgentTaskNamePrefix,
	}, document.ListOptions{Limit: 1000})
	if err != nil {
		return nil, err
	}
	out := []scheduledTask{}
	for _, d := range res.Items {
		got, gerr := documentStore.Get(ctx, document.Key{Scope: document.ScopeWorkspace, WorkspaceID: d.WorkspaceID, Name: d.Name}, document.GetOptions{})
		if gerr != nil || len(got.Versions) == 0 {
			continue
		}
		var cfg taskConfig
		if json.Unmarshal([]byte(got.Versions[0].Body), &cfg) != nil {
			continue // skip a malformed config rather than failing the whole pass
		}
		if cfg.Trigger != triggerSchedule {
			continue
		}
		out = append(out, scheduledTask{
			WorkspaceID: d.WorkspaceID,
			Name:        strings.TrimPrefix(d.Name, synth.AgentTaskNamePrefix),
			Config:      cfg,
		})
	}
	return out, nil
}

// defaultRunRecorder appends one ledger row per scheduled run (success,
// not-run, or error) so each cadence tick is observable. A nil store → no-op.
func defaultRunRecorder(ledgerStore *ledger.Store) func(context.Context, scheduledTask, WorkspaceTaskRunResponse, error) {
	return func(ctx context.Context, t scheduledTask, resp WorkspaceTaskRunResponse, runErr error) {
		if ledgerStore == nil {
			return
		}
		var body string
		switch {
		case runErr != nil:
			body = fmt.Sprintf("scheduled run of %q failed: %v", t.Name, runErr)
		case !resp.Ran:
			body = fmt.Sprintf("scheduled run of %q not-run: %s", t.Name, resp.Note)
		default:
			body = fmt.Sprintf("scheduled run of %q → %s", t.Name, resp.OutputName)
		}
		if _, err := ledgerStore.Append(ctx, ledger.AppendInput{
			WorkspaceID: t.WorkspaceID,
			Kind:        ledgerKindTaskRun,
			Actor:       "scheduler",
			Body:        body,
		}, time.Now().UTC()); err != nil {
			arbor.WarnCtx(ctx, "task scheduler: ledger append", "err", err, "task", t.Name)
		}
	}
}

// ledgerKindTaskRun marks a scheduled agent-task run on the ledger.
const ledgerKindTaskRun = "task_run"
