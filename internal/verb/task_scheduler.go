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

// TaskScheduler runs trigger-driven agent-tasks: schedule (on an interval) and
// on_document_change (when the workspace corpus changes). Its seams (list,
// probe, run, record, now) are injectable so tests drive it deterministically
// without a database, network, or real clock.
type TaskScheduler struct {
	interval time.Duration // tick granularity
	now      func() time.Time
	list     func(ctx context.Context) ([]scheduledTask, error)
	probe    func(ctx context.Context, wsID, excludeName string) (time.Time, error)
	run      func(ctx context.Context, t scheduledTask) (WorkspaceTaskRunResponse, error)
	record   func(ctx context.Context, t scheduledTask, resp WorkspaceTaskRunResponse, runErr error)

	mu           sync.Mutex
	nextRun      map[string]time.Time // schedule trigger: next interval due time
	docWatermark map[string]time.Time // on_document_change: last corpus-change time acted on
	running      map[string]bool
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
		probe:    defaultDocChangeProbe,
		run: func(ctx context.Context, t scheduledTask) (WorkspaceTaskRunResponse, error) {
			return runWorkspaceTask(ctx, t.WorkspaceID, t.Config.TaskSkill, t.Config.OutputName, t.Config.Executor, "scheduler")
		},
		record:       defaultRunRecorder(ledgerStore),
		nextRun:      map[string]time.Time{},
		docWatermark: map[string]time.Time{},
		running:      map[string]bool{},
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

// tick discovers trigger-driven tasks and launches a single-flight run for each
// that is due, dispatching by trigger. ticks do not overlap (Run calls tick
// synchronously), so the check-then-mark below races only against the launched
// runs, which only ever clear `running` and advance state under the lock.
func (s *TaskScheduler) tick(ctx context.Context) {
	tasks, err := s.list(ctx)
	if err != nil {
		arbor.WarnCtx(ctx, "task scheduler: list", "err", err)
		return
	}
	for _, t := range tasks {
		switch t.Config.Trigger {
		case triggerSchedule:
			s.considerSchedule(ctx, t)
		case triggerOnDocChange:
			s.considerDocChange(ctx, t)
		}
	}
}

// considerSchedule runs an interval task when now ≥ its next_run_at. A task seen
// for the first time is deferred one interval out (no immediate run, so a
// restart does not re-fire every task).
func (s *TaskScheduler) considerSchedule(ctx context.Context, t scheduledTask) {
	if t.Config.Schedule == nil || t.Config.Schedule.IntervalSeconds <= 0 {
		return
	}
	key := t.WorkspaceID + "/" + t.Name
	now := s.now()
	s.mu.Lock()
	if s.running[key] {
		s.mu.Unlock()
		return // single-flight
	}
	next, seen := s.nextRun[key]
	if !seen {
		s.nextRun[key] = now.Add(s.intervalOf(t)) // defer first run
		s.mu.Unlock()
		return
	}
	if now.Before(next) {
		s.mu.Unlock()
		return // not due yet
	}
	s.running[key] = true
	s.mu.Unlock()
	interval := s.intervalOf(t)
	s.launch(ctx, t, key, func() { s.nextRun[key] = s.now().Add(interval) })
}

// considerDocChange runs a reactive task when the workspace corpus has changed
// since the last run. A task seen for the first time establishes a baseline (no
// run); thereafter a probed change time later than the watermark fires one run.
// The probe excludes the task's own output, so writing the result never
// re-fires (no self-trigger loop).
func (s *TaskScheduler) considerDocChange(ctx context.Context, t scheduledTask) {
	key := t.WorkspaceID + "/" + t.Name
	s.mu.Lock()
	running := s.running[key]
	s.mu.Unlock()
	if running {
		return // single-flight
	}
	latest, err := s.probe(ctx, t.WorkspaceID, t.Config.OutputName)
	if err != nil {
		arbor.WarnCtx(ctx, "task scheduler: change probe", "workspace_id", t.WorkspaceID, "task", t.Name, "err", err)
		return
	}
	s.mu.Lock()
	wm, seen := s.docWatermark[key]
	if !seen {
		s.docWatermark[key] = latest // baseline, no run
		s.mu.Unlock()
		return
	}
	if !latest.After(wm) {
		s.mu.Unlock()
		return // no change since last run
	}
	s.running[key] = true
	s.mu.Unlock()
	s.launch(ctx, t, key, func() { s.docWatermark[key] = latest })
}

// launch runs a task in its own goroutine, records it, then advances the
// trigger's per-task state and releases the single-flight lock — even when the
// run errors or reports not-run, so a failure never wedges the task. advance
// runs under the lock.
func (s *TaskScheduler) launch(ctx context.Context, t scheduledTask, key string, advance func()) {
	go func() {
		resp, runErr := s.run(ctx, t)
		if s.record != nil {
			s.record(ctx, t, resp, runErr)
		}
		s.mu.Lock()
		advance()
		delete(s.running, key)
		s.mu.Unlock()
	}()
}

func (s *TaskScheduler) intervalOf(t scheduledTask) time.Duration {
	return time.Duration(t.Config.Schedule.IntervalSeconds) * time.Second
}

// defaultScheduledTaskLister discovers agent-task documents across every
// workspace (no workspace_id filter) and returns those with a worker-driven
// trigger (schedule or on_document_change). on_demand tasks are skipped — they
// run only on an explicit verb call. The config body is read per task (List
// does not carry bodies).
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
		if cfg.Trigger != triggerSchedule && cfg.Trigger != triggerOnDocChange {
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

// defaultDocChangeProbe returns the most recent change time across a
// workspace's corpus documents, EXCLUDING the task's own output (so writing the
// result never re-fires the task) and the agent-task/* config namespace (config
// edits are not corpus changes). A zero time means no eligible document.
func defaultDocChangeProbe(ctx context.Context, wsID, excludeName string) (time.Time, error) {
	if documentStore == nil {
		return time.Time{}, nil
	}
	res, err := documentStore.List(ctx, document.ListFilter{
		Type:        document.TypeDocument,
		Scope:       document.ScopeWorkspace,
		WorkspaceID: wsID,
	}, document.ListOptions{Limit: 1000})
	if err != nil {
		return time.Time{}, err
	}
	var latest time.Time
	for _, d := range res.Items {
		if d.Name == excludeName || strings.HasPrefix(d.Name, synth.AgentTaskNamePrefix) {
			continue
		}
		if d.UpdatedAt.After(latest) {
			latest = d.UpdatedAt
		}
	}
	return latest, nil
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
