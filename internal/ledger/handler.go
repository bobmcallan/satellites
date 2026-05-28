// LedgerHandler — slog.Handler that persists log records as evidence
// ledger rows when the record's context carries operator-observability
// ids. Background:
//
//   - The producer side (Handle) is non-blocking — observability must
//     never become load-bearing for request latency.
//   - The drainer side batches inserts (100 events OR 250ms whichever
//     first) so a burst of arbor calls inside one request amortises
//     into a single multi-row INSERT.
//   - Overflow policy is drop-oldest. Recent observability is the part
//     the operator cares about during incident triage.
//   - DB errors are bounded-retried (exponential backoff to 2s, three
//     attempts per batch) then the batch is dropped with a loud
//     counter so the operator notices gaps.
//   - Shutdown is cooperative: Close() signals the drainer to flush
//     pending events with a 5s budget.
//
// The handler skips records whose context has no correlation IDs
// stamped — that's the deliberate cutoff for "is this a run-scoped
// log worth keeping?" Server background work (boot, migrations, idle
// ticks) therefore does not flood the ledger.

package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bobmcallan/satellites/internal/correlation"
)

// HandlerOptions configures a LedgerHandler. Zero values default to
// production-sensible numbers; tests override via explicit fields.
type HandlerOptions struct {
	// MinLevel filters records below this level before they reach the
	// buffer. Defaults to slog.LevelInfo.
	MinLevel slog.Level

	// BufferSize is the bounded channel depth. Defaults to 4096.
	BufferSize int

	// BatchSize caps one INSERT batch. Defaults to 100.
	BatchSize int

	// BatchInterval is the flush deadline per batch. Defaults to 250ms.
	BatchInterval time.Duration

	// MaxRetries per batch on DB error. Defaults to 3.
	MaxRetries int

	// BackoffStart is the initial retry delay. Defaults to 50ms.
	BackoffStart time.Duration

	// BackoffMax caps the exponential delay. Defaults to 2s.
	BackoffMax time.Duration

	// DropCounterInterval is the period at which non-zero drop counters
	// are logged to stderr (via the non-recursive fallback writer so
	// the log does not recurse through this handler). Defaults to 30s.
	DropCounterInterval time.Duration

	// Now is injectable for tests. Defaults to time.Now.
	Now func() time.Time

	// FallbackLog is the loud-but-non-recursive sink used to surface
	// drop counters and unrecoverable insert errors. Defaults to a
	// log.Default-style writer to os.Stderr.
	FallbackLog func(msg string, args ...any)
}

// AppendManyer is the subset of Store used by LedgerHandler. Exported
// so other packages (notably integration / E2E tests) can construct
// a LedgerHandler against an in-memory implementation; production
// callers pass *Store.
type AppendManyer interface {
	AppendMany(ctx context.Context, ins []AppendInput, now time.Time) error
}

// LedgerHandler is the slog.Handler that captures records for the
// evidence ledger. Construct via NewHandler.
type LedgerHandler struct {
	store AppendManyer
	opts  HandlerOptions
	attrs []slog.Attr
	group string

	queue chan AppendInput

	wg     sync.WaitGroup
	closed chan struct{}
	once   sync.Once

	droppedOverflow    atomic.Uint64
	droppedInsertError atomic.Uint64
	written            atomic.Uint64
}

// NewHandler constructs a LedgerHandler and starts its drainer
// goroutine. The caller must invoke Close to drain the queue before
// process exit.
func NewHandler(store AppendManyer, opts HandlerOptions) *LedgerHandler {
	if opts.BufferSize <= 0 {
		opts.BufferSize = 4096
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 100
	}
	if opts.BatchInterval <= 0 {
		opts.BatchInterval = 250 * time.Millisecond
	}
	if opts.MaxRetries <= 0 {
		opts.MaxRetries = 3
	}
	if opts.BackoffStart <= 0 {
		opts.BackoffStart = 50 * time.Millisecond
	}
	if opts.BackoffMax <= 0 {
		opts.BackoffMax = 2 * time.Second
	}
	if opts.DropCounterInterval <= 0 {
		opts.DropCounterInterval = 30 * time.Second
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.FallbackLog == nil {
		opts.FallbackLog = defaultFallbackLog
	}
	h := &LedgerHandler{
		store:  store,
		opts:   opts,
		queue:  make(chan AppendInput, opts.BufferSize),
		closed: make(chan struct{}),
	}
	h.wg.Add(2)
	go h.drainLoop()
	go h.dropReporter()
	return h
}

// Enabled gates records by the configured MinLevel. The handler is
// always interested in records *with* correlation ids stamped, but
// the slog.Handler contract requires this check; pre-filtering at
// MinLevel keeps the chan free of debug noise in production.
func (h *LedgerHandler) Enabled(_ context.Context, lvl slog.Level) bool {
	return lvl >= h.opts.MinLevel
}

// Handle is the producer entry point — non-blocking; never returns an
// error to the caller.
//
// Records with no correlation ids are silently skipped. Records that
// arrive after Close are dropped (the channel is closed; the select
// default branch fires).
func (h *LedgerHandler) Handle(ctx context.Context, r slog.Record) error {
	ids, ok := correlation.FromContext(ctx)
	if !ok || ids.Empty() {
		return nil
	}
	in := h.buildInput(ids, r)
	for {
		select {
		case <-h.closed:
			// Closed mid-flight; new records are dropped without a counter
			// bump (graceful shutdown is not "overflow").
			return nil
		case h.queue <- in:
			return nil
		default:
			// Overflow: drop the OLDEST in the queue, then retry. Two
			// non-blocking selects in sequence rather than one blocking
			// select so we never park the producer.
			select {
			case <-h.queue:
				h.droppedOverflow.Add(1)
			default:
				// Race: someone else drained between our default branch and
				// here. Loop and try the push again.
			}
		}
	}
}

// WithAttrs / WithGroup follow slog's contract by capturing the
// attrs/group on a new handler that shares the same drainer.
func (h *LedgerHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	combined := append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &LedgerHandler{
		store: h.store, opts: h.opts, attrs: combined, group: h.group,
		queue: h.queue, closed: h.closed,
	}
}

func (h *LedgerHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	group := h.group
	if group == "" {
		group = name
	} else {
		group = group + "." + name
	}
	return &LedgerHandler{
		store: h.store, opts: h.opts, attrs: h.attrs, group: group,
		queue: h.queue, closed: h.closed,
	}
}

// Close signals the drainer to stop accepting new events and drain
// the queue with a 5-second budget. Safe to call once; subsequent
// calls are no-ops.
func (h *LedgerHandler) Close(ctx context.Context) error {
	h.once.Do(func() {
		close(h.closed)
	})
	done := make(chan struct{})
	go func() { h.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
		return errors.New("ledger handler: shutdown drain timed out after 5s")
	}
}

// Stats exposes the counters for the operator observability of
// observability itself.
type Stats struct {
	EventsWritten            uint64
	EventsDroppedOverflow    uint64
	EventsDroppedInsertError uint64
	QueueDepth               int
}

// Stats returns a snapshot of the counters.
func (h *LedgerHandler) Stats() Stats {
	return Stats{
		EventsWritten:            h.written.Load(),
		EventsDroppedOverflow:    h.droppedOverflow.Load(),
		EventsDroppedInsertError: h.droppedInsertError.Load(),
		QueueDepth:               len(h.queue),
	}
}

// buildInput renders an arbor slog.Record into an AppendInput. Kind
// is "log:<level>" so the runner-role gate (verb-side) can accept it
// without needing to know slog.Level integer values.
func (h *LedgerHandler) buildInput(ids correlation.IDs, r slog.Record) AppendInput {
	kind := LogKindPrefix + strings.ToLower(r.Level.String())
	// Marshal attrs (record + handler-attached) as the payload. Using
	// json.RawMessage avoids a second JSON round-trip downstream.
	payload := h.marshalAttrs(r)
	return AppendInput{
		StoryID:     ids.StoryID,
		ProjectID:   ids.ProjectID,
		WorkspaceID: ids.WorkspaceID,
		SessionID:   ids.SessionID,
		RunID:       ids.RunID,
		Kind:        kind,
		Body:        r.Message,
		Payload:     payload,
	}
}

func (h *LedgerHandler) marshalAttrs(r slog.Record) json.RawMessage {
	m := map[string]any{}
	for _, a := range h.attrs {
		m[a.Key] = a.Value.Resolve().Any()
	}
	r.Attrs(func(a slog.Attr) bool {
		m[a.Key] = a.Value.Resolve().Any()
		return true
	})
	if h.group != "" {
		m = map[string]any{h.group: m}
	}
	b, err := json.Marshal(m)
	if err != nil {
		// Fallback to empty payload — never let a marshal error block
		// the ledger row. The body still carries the message.
		return json.RawMessage("{}")
	}
	return b
}

func (h *LedgerHandler) drainLoop() {
	defer h.wg.Done()
	ticker := time.NewTicker(h.opts.BatchInterval)
	defer ticker.Stop()

	batch := make([]AppendInput, 0, h.opts.BatchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		h.flushBatch(batch)
		batch = batch[:0]
	}

	for {
		select {
		case <-h.closed:
			// Drain whatever's in the queue under a 5s overall budget.
			// We exit when either the queue is empty or the budget elapses.
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				select {
				case in := <-h.queue:
					batch = append(batch, in)
					if len(batch) >= h.opts.BatchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
			flush()
			return
		case in := <-h.queue:
			batch = append(batch, in)
			if len(batch) >= h.opts.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (h *LedgerHandler) flushBatch(batch []AppendInput) {
	delay := h.opts.BackoffStart
	for attempt := 1; attempt <= h.opts.MaxRetries; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := h.store.AppendMany(ctx, batch, h.opts.Now())
		cancel()
		if err == nil {
			h.written.Add(uint64(len(batch)))
			return
		}
		if attempt == h.opts.MaxRetries {
			h.droppedInsertError.Add(uint64(len(batch)))
			h.opts.FallbackLog("ledger handler: batch dropped after retries",
				"batch_size", len(batch), "err", err.Error())
			return
		}
		time.Sleep(delay)
		delay *= 2
		if delay > h.opts.BackoffMax {
			delay = h.opts.BackoffMax
		}
	}
}

func (h *LedgerHandler) dropReporter() {
	defer h.wg.Done()
	ticker := time.NewTicker(h.opts.DropCounterInterval)
	defer ticker.Stop()
	for {
		select {
		case <-h.closed:
			return
		case <-ticker.C:
			over := h.droppedOverflow.Load()
			ins := h.droppedInsertError.Load()
			if over == 0 && ins == 0 {
				continue
			}
			h.opts.FallbackLog("ledger handler: drop counters non-zero",
				"events_dropped_overflow", over,
				"events_dropped_insert_error", ins,
				"events_written", h.written.Load(),
				"queue_depth", len(h.queue),
			)
		}
	}
}

// defaultFallbackLog routes to stderr via the standard log package so
// we never recurse through arbor's slog handler chain (which would
// route back through this handler and either deadlock the channel or
// cause unbounded recursion on a DB outage).
func defaultFallbackLog(msg string, args ...any) {
	pairs := make([]string, 0, len(args)/2)
	for i := 0; i+1 < len(args); i += 2 {
		pairs = append(pairs, fmt.Sprintf("%v=%v", args[i], args[i+1]))
	}
	fmt.Fprintf(stderrFallbackWriter, "ledger-handler: %s %s\n", msg, strings.Join(pairs, " "))
}
