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

func docChangeFixture() scheduledTask {
	return scheduledTask{
		WorkspaceID: "w1",
		Name:        "t1",
		Config:      taskConfig{Trigger: triggerOnDocChange, OutputName: "out"},
	}
}

// fakeProbe is a mutex-guarded corpus-change time the tests control.
type fakeProbe struct {
	mu sync.Mutex
	t  time.Time
}

func (p *fakeProbe) probe(context.Context, string, string) (time.Time, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.t, nil
}

func (p *fakeProbe) set(t time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.t = t
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

// TestTaskScheduler_DocChange_BaselineThenRun: an on_document_change task does
// not run on first sight (baseline); it runs once the corpus changes past the
// watermark, and a later tick with no further change does not re-fire.
func TestTaskScheduler_DocChange_BaselineThenRun(t *testing.T) {
	base := time.Unix(4_000_000, 0).UTC()
	pr := &fakeProbe{}
	pr.set(base)
	var runCount int32
	s := &TaskScheduler{
		now:   time.Now,
		list:  func(context.Context) ([]scheduledTask, error) { return []scheduledTask{docChangeFixture()}, nil },
		probe: pr.probe,
		run: func(context.Context, scheduledTask) (WorkspaceTaskRunResponse, error) {
			atomic.AddInt32(&runCount, 1)
			return WorkspaceTaskRunResponse{Ran: true, OutputName: "out"}, nil
		},
		nextRun:      map[string]time.Time{},
		docWatermark: map[string]time.Time{},
		running:      map[string]bool{},
	}

	s.tick(context.Background()) // first sight → baseline, no run
	time.Sleep(40 * time.Millisecond)
	if got := atomic.LoadInt32(&runCount); got != 0 {
		t.Fatalf("ran on first sight (no baseline): runCount=%d", got)
	}

	pr.set(base.Add(time.Minute)) // a corpus document changed
	s.tick(context.Background())
	waitFor(t, "run after change", func() bool { return atomic.LoadInt32(&runCount) == 1 })

	// No further change → no re-fire.
	s.tick(context.Background())
	time.Sleep(40 * time.Millisecond)
	if got := atomic.LoadInt32(&runCount); got != 1 {
		t.Fatalf("re-fired with no change: runCount=%d", got)
	}
}

// TestTaskScheduler_DocChange_Coalesce: several edits landing before one tick
// (the probe jumps once) yield a single run — the debounce window is the tick.
func TestTaskScheduler_DocChange_Coalesce(t *testing.T) {
	base := time.Unix(5_000_000, 0).UTC()
	pr := &fakeProbe{}
	pr.set(base)
	var runCount int32
	s := &TaskScheduler{
		now:   time.Now,
		list:  func(context.Context) ([]scheduledTask, error) { return []scheduledTask{docChangeFixture()}, nil },
		probe: pr.probe,
		run: func(context.Context, scheduledTask) (WorkspaceTaskRunResponse, error) {
			atomic.AddInt32(&runCount, 1)
			return WorkspaceTaskRunResponse{Ran: true, OutputName: "out"}, nil
		},
		nextRun:      map[string]time.Time{},
		docWatermark: map[string]time.Time{},
		running:      map[string]bool{},
	}
	s.tick(context.Background()) // baseline

	// Two rapid edits before the next tick → probe reflects only the latest.
	pr.set(base.Add(3 * time.Second))
	s.tick(context.Background())
	waitFor(t, "single coalesced run", func() bool { return atomic.LoadInt32(&runCount) == 1 })
	// Let the run settle; the watermark advances to the coalesced change time.
	waitFor(t, "running cleared", func() bool { s.mu.Lock(); defer s.mu.Unlock(); return !s.running["w1/t1"] })
	s.tick(context.Background())
	time.Sleep(40 * time.Millisecond)
	if got := atomic.LoadInt32(&runCount); got != 1 {
		t.Fatalf("coalesce failed: expected 1 run, got %d", got)
	}
}

// TestTaskScheduler_DocChange_FailureDoesNotWedge: a failed reactive run is
// survived — the watermark still advances and a later change fires again.
func TestTaskScheduler_DocChange_FailureDoesNotWedge(t *testing.T) {
	base := time.Unix(6_000_000, 0).UTC()
	pr := &fakeProbe{}
	pr.set(base)
	var runCount int32
	s := &TaskScheduler{
		now:   time.Now,
		list:  func(context.Context) ([]scheduledTask, error) { return []scheduledTask{docChangeFixture()}, nil },
		probe: pr.probe,
		run: func(context.Context, scheduledTask) (WorkspaceTaskRunResponse, error) {
			atomic.AddInt32(&runCount, 1)
			return WorkspaceTaskRunResponse{}, errors.New("boom")
		},
		record:       func(context.Context, scheduledTask, WorkspaceTaskRunResponse, error) {},
		nextRun:      map[string]time.Time{},
		docWatermark: map[string]time.Time{},
		running:      map[string]bool{},
	}
	s.tick(context.Background()) // baseline

	pr.set(base.Add(time.Minute))
	s.tick(context.Background())
	waitFor(t, "first failing run", func() bool { return atomic.LoadInt32(&runCount) == 1 })
	waitFor(t, "watermark advanced + running cleared", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return !s.running["w1/t1"] && !s.docWatermark["w1/t1"].Before(base.Add(time.Minute))
	})

	pr.set(base.Add(2 * time.Minute)) // another change
	s.tick(context.Background())
	waitFor(t, "second run after failure", func() bool { return atomic.LoadInt32(&runCount) == 2 })
}
