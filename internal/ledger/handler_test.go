package ledger

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/correlation"
)

// fakeStore is an in-memory appendManyer used by handler tests.
type fakeStore struct {
	mu       sync.Mutex
	batches  [][]AppendInput
	failures int           // number of times to return an error before succeeding
	delay    time.Duration // sleep before responding (simulate slow DB)
	err      error
}

func (f *fakeStore) AppendMany(ctx context.Context, ins []AppendInput, _ time.Time) error {
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failures > 0 {
		f.failures--
		return errors.New("simulated db error")
	}
	if f.err != nil {
		return f.err
	}
	// Copy the slice so the test can read it independently of later
	// drainer reuse.
	cp := append([]AppendInput(nil), ins...)
	f.batches = append(f.batches, cp)
	return nil
}

func (f *fakeStore) snapshot() [][]AppendInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]AppendInput, len(f.batches))
	for i, b := range f.batches {
		cp := append([]AppendInput(nil), b...)
		out[i] = cp
	}
	return out
}

func (f *fakeStore) totalRows() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, b := range f.batches {
		n += len(b)
	}
	return n
}

// recordWithCtx is a helper to emit a Record with stamped correlation.
func sendOne(t *testing.T, h *LedgerHandler, runID, msg string) {
	t.Helper()
	ctx := correlation.WithIDs(context.Background(), correlation.IDs{RunID: runID})
	r := slog.NewRecord(time.Now(), slog.LevelInfo, msg, 0)
	if err := h.Handle(ctx, r); err != nil {
		t.Fatalf("Handle: %v", err)
	}
}

func TestHandler_SkipsRecordsWithoutCorrelation(t *testing.T) {
	store := &fakeStore{}
	h := NewHandler(store, HandlerOptions{BatchInterval: 20 * time.Millisecond})
	defer h.Close(context.Background())

	r := slog.NewRecord(time.Now(), slog.LevelInfo, "no-ids", 0)
	if err := h.Handle(context.Background(), r); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	// Stamp empty IDs explicitly.
	ctx := correlation.WithIDs(context.Background(), correlation.IDs{})
	if err := h.Handle(ctx, r); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if store.totalRows() != 0 {
		t.Fatalf("expected 0 rows, got %d", store.totalRows())
	}
}

func TestHandler_BatchesBySize(t *testing.T) {
	store := &fakeStore{}
	h := NewHandler(store, HandlerOptions{
		BatchSize:     3,
		BatchInterval: time.Second, // far enough away that size triggers first
	})
	defer h.Close(context.Background())

	for i := 0; i < 5; i++ {
		sendOne(t, h, "run_a", "msg")
	}
	// Wait for the drainer to flush at the size threshold.
	deadline := time.Now().Add(500 * time.Millisecond)
	for store.totalRows() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if store.totalRows() < 3 {
		t.Fatalf("expected at least 3 rows by size flush, got %d", store.totalRows())
	}
	// And a final close flushes the remainder.
	_ = h.Close(context.Background())
	if got := store.totalRows(); got != 5 {
		t.Fatalf("expected 5 rows after close, got %d", got)
	}
}

func TestHandler_BatchesByInterval(t *testing.T) {
	store := &fakeStore{}
	h := NewHandler(store, HandlerOptions{
		BatchSize:     100,
		BatchInterval: 30 * time.Millisecond,
	})
	defer h.Close(context.Background())

	sendOne(t, h, "run_b", "one")
	// Wait beyond one interval. The drainer should flush the single
	// queued event.
	time.Sleep(120 * time.Millisecond)
	if store.totalRows() != 1 {
		t.Fatalf("expected 1 row after interval flush, got %d", store.totalRows())
	}
}

func TestHandler_DropOldestOnOverflow(t *testing.T) {
	// Block the drainer by giving the store a long delay so the queue
	// fills before anything drains.
	store := &fakeStore{delay: 5 * time.Second}
	h := NewHandler(store, HandlerOptions{
		BufferSize:    4,
		BatchSize:     1,
		BatchInterval: time.Second,
	})
	defer h.Close(context.Background())

	// Push 20 events; only the last 4 should survive in the queue (the
	// rest dropped as oldest).
	for i := 0; i < 20; i++ {
		sendOne(t, h, "run_c", "x")
	}
	// Producer must never block; reaching this line is the test.
	if got := h.Stats().EventsDroppedOverflow; got < 10 {
		t.Fatalf("expected substantial overflow drops, got %d", got)
	}
}

func TestHandler_RetryThenDrop(t *testing.T) {
	// Always-failing store. Handler should retry MaxRetries times then
	// bump the insert-error counter for the whole batch.
	store := &fakeStore{failures: 999}
	h := NewHandler(store, HandlerOptions{
		BatchSize:     1,
		BatchInterval: 10 * time.Millisecond,
		MaxRetries:    3,
		BackoffStart:  1 * time.Millisecond,
		BackoffMax:    5 * time.Millisecond,
	})
	defer h.Close(context.Background())

	sendOne(t, h, "run_d", "fail-me")
	deadline := time.Now().Add(500 * time.Millisecond)
	for h.Stats().EventsDroppedInsertError == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if h.Stats().EventsDroppedInsertError == 0 {
		t.Fatalf("expected insert-error drop, stats=%+v", h.Stats())
	}
}

func TestHandler_RetryThenSucceed(t *testing.T) {
	// Fail twice, succeed on the third attempt.
	store := &fakeStore{failures: 2}
	h := NewHandler(store, HandlerOptions{
		BatchSize:     1,
		BatchInterval: 10 * time.Millisecond,
		MaxRetries:    3,
		BackoffStart:  1 * time.Millisecond,
		BackoffMax:    5 * time.Millisecond,
	})
	defer h.Close(context.Background())

	sendOne(t, h, "run_e", "transient")
	deadline := time.Now().Add(500 * time.Millisecond)
	for store.totalRows() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if store.totalRows() != 1 {
		t.Fatalf("expected 1 row after retry success, got %d", store.totalRows())
	}
	if h.Stats().EventsDroppedInsertError != 0 {
		t.Fatalf("no drops expected, got %+v", h.Stats())
	}
}

func TestHandler_CloseDrainsPending(t *testing.T) {
	store := &fakeStore{}
	h := NewHandler(store, HandlerOptions{
		BatchSize:     50,
		BatchInterval: time.Second,
	})
	for i := 0; i < 7; i++ {
		sendOne(t, h, "run_f", "m")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := h.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := store.totalRows(); got != 7 {
		t.Fatalf("expected 7 rows drained on Close, got %d", got)
	}
}

func TestHandler_ProducerIsNonBlockingUnderLoad(t *testing.T) {
	// Drainer is slow; producer should still return well under the
	// per-Handle budget set here.
	store := &fakeStore{delay: 100 * time.Millisecond}
	h := NewHandler(store, HandlerOptions{
		BufferSize:    16,
		BatchSize:     1,
		BatchInterval: time.Second,
	})
	defer h.Close(context.Background())

	var completed atomic.Uint64
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			sendOne(t, h, "run_g", "load")
			if time.Since(start) > 50*time.Millisecond {
				t.Errorf("producer blocked for %v (drainer should not back-pressure)", time.Since(start))
				return
			}
			completed.Add(1)
		}()
	}
	wg.Wait()
	if completed.Load() != 20 {
		t.Fatalf("only %d/20 producers completed", completed.Load())
	}
}

func TestHandler_KindCarriesLogPrefix(t *testing.T) {
	store := &fakeStore{}
	h := NewHandler(store, HandlerOptions{
		BatchSize:     1,
		BatchInterval: 10 * time.Millisecond,
	})
	defer h.Close(context.Background())

	ctx := correlation.WithIDs(context.Background(), correlation.IDs{
		RunID:     "run_h",
		StoryID:   "sty_1",
		ProjectID: "proj_1",
	})
	r := slog.NewRecord(time.Now(), slog.LevelWarn, "warn msg", 0)
	if err := h.Handle(ctx, r); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for store.totalRows() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	batches := store.snapshot()
	if len(batches) == 0 || len(batches[0]) == 0 {
		t.Fatal("no row written")
	}
	row := batches[0][0]
	if row.Kind != "log:warn" {
		t.Fatalf("expected kind=log:warn, got %q", row.Kind)
	}
	if row.RunID != "run_h" || row.StoryID != "sty_1" || row.ProjectID != "proj_1" {
		t.Fatalf("ids mismatch: %+v", row)
	}
	if row.Body != "warn msg" {
		t.Fatalf("body mismatch: %q", row.Body)
	}
}
