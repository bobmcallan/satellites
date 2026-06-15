package verb

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock is a mutex-guarded clock the tests advance manually.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

func scheduledFixture() scheduledTask {
	return scheduledTask{
		WorkspaceID: "w1",
		Name:        "t1",
		Config:      taskConfig{Trigger: triggerSchedule, OutputName: "out", Schedule: &taskSchedule{IntervalSeconds: 60}},
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestTaskScheduler_FirstSightDefersThenRuns: a newly-seen scheduled task does
// not run on first sight (so a restart does not re-fire it); it runs once the
// clock passes its deferred next_run_at.
func TestTaskScheduler_FirstSightDefersThenRuns(t *testing.T) {
	clock := &fakeClock{}
	base := time.Unix(1_000_000, 0).UTC()
	clock.set(base)
	runs := make(chan scheduledTask, 4)
	s := &TaskScheduler{
		now:  clock.now,
		list: func(context.Context) ([]scheduledTask, error) { return []scheduledTask{scheduledFixture()}, nil },
		run: func(_ context.Context, t scheduledTask) (WorkspaceTaskRunResponse, error) {
			runs <- t
			return WorkspaceTaskRunResponse{Ran: true, OutputName: "out"}, nil
		},
		nextRun: map[string]time.Time{},
		running: map[string]bool{},
	}

	s.tick(context.Background()) // first sight → defer, no run
	select {
	case <-runs:
		t.Fatal("scheduled task ran on first sight; should defer one interval")
	case <-time.After(50 * time.Millisecond):
	}

	clock.set(base.Add(61 * time.Second)) // past the deferred next_run_at
	s.tick(context.Background())
	select {
	case got := <-runs:
		if got.Name != "t1" {
			t.Fatalf("ran wrong task: %q", got.Name)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scheduled task did not run when due")
	}
}

// TestTaskScheduler_SingleFlight: while a run is in flight, another tick does
// not launch a second run of the same task.
func TestTaskScheduler_SingleFlight(t *testing.T) {
	clock := &fakeClock{}
	clock.set(time.Unix(2_000_000, 0).UTC())
	block := make(chan struct{})
	var runCount int32
	s := &TaskScheduler{
		now:  clock.now,
		list: func(context.Context) ([]scheduledTask, error) { return []scheduledTask{scheduledFixture()}, nil },
		run: func(context.Context, scheduledTask) (WorkspaceTaskRunResponse, error) {
			atomic.AddInt32(&runCount, 1)
			<-block // hold the run open
			return WorkspaceTaskRunResponse{Ran: true, OutputName: "out"}, nil
		},
		nextRun: map[string]time.Time{"w1/t1": clock.now().Add(-time.Second)}, // already due
		running: map[string]bool{},
	}

	s.tick(context.Background()) // launches the (blocking) run
	waitFor(t, "first run to start", func() bool { return atomic.LoadInt32(&runCount) == 1 })

	s.tick(context.Background()) // task still running → must NOT launch a second
	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt32(&runCount); got != 1 {
		t.Fatalf("single-flight violated: run launched %d times while one was in flight", got)
	}

	close(block) // let the run finish
	waitFor(t, "running flag cleared", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return !s.running["w1/t1"]
	})
}

// TestTaskScheduler_FailureDoesNotWedge: a run error is survived — next_run_at
// still advances and a later due tick runs the task again.
func TestTaskScheduler_FailureDoesNotWedge(t *testing.T) {
	clock := &fakeClock{}
	base := time.Unix(3_000_000, 0).UTC()
	clock.set(base)
	var runCount int32
	var recorded []error
	var recMu sync.Mutex
	s := &TaskScheduler{
		now:  clock.now,
		list: func(context.Context) ([]scheduledTask, error) { return []scheduledTask{scheduledFixture()}, nil },
		run: func(context.Context, scheduledTask) (WorkspaceTaskRunResponse, error) {
			atomic.AddInt32(&runCount, 1)
			return WorkspaceTaskRunResponse{}, errors.New("boom")
		},
		record: func(_ context.Context, _ scheduledTask, _ WorkspaceTaskRunResponse, runErr error) {
			recMu.Lock()
			recorded = append(recorded, runErr)
			recMu.Unlock()
		},
		nextRun: map[string]time.Time{"w1/t1": base.Add(-time.Second)}, // due now
		running: map[string]bool{},
	}

	s.tick(context.Background()) // run #1 → errors
	waitFor(t, "first failing run recorded", func() bool {
		recMu.Lock()
		defer recMu.Unlock()
		return len(recorded) == 1 && recorded[0] != nil
	})
	// next_run_at must have advanced ~one interval out and running cleared.
	waitFor(t, "next_run_at advanced after failure", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return !s.running["w1/t1"] && s.nextRun["w1/t1"].After(base)
	})

	clock.set(base.Add(61 * time.Second)) // due again
	s.tick(context.Background())
	waitFor(t, "second run after failure", func() bool { return atomic.LoadInt32(&runCount) == 2 })
}
