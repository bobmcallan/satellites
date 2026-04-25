// Package embedworker is the satellites-v4 embedding ingestion worker.
// It consumes embed-document and embed-ledger tasks from the task queue,
// fetches the parent row, chunks the body, embeds via the configured
// embeddings.Embedder, and writes chunks to the per-primitive ChunkStore.
//
// Failures retry up to MaxRetries; on retry exhaustion the worker writes
// a `kind:embedding-failure` ledger row carrying the source primitive id
// + the provider error so the failure is auditable.
//
// One Worker drives both task kinds. Multiple workers can run in parallel
// — the task store's Close transition is the serialisation point (a stale
// Close from a worker that lost the race fails with ErrInvalidTransition).
package embedworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ternarybob/arbor"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/embeddings"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/task"
)

// MaxRetries caps the worker's per-task retry budget. Beyond this an
// embedding-failure ledger row is written and the task is closed with
// outcome=failure.
const MaxRetries = 3

// DefaultPollInterval is how long the worker sleeps between empty
// queue scans. Short enough that human-perceptible latency on doc
// creation stays in the low seconds.
const DefaultPollInterval = 500 * time.Millisecond

// Deps bundles the worker's runtime dependencies. All non-Logger fields
// are required.
type Deps struct {
	Tasks        task.Store
	Embedder     embeddings.Embedder
	Docs         document.Store
	DocChunks    document.ChunkStore
	Ledger       ledger.Store
	LedgerChunks ledger.ChunkStore
	Logger       arbor.ILogger

	// PollInterval overrides DefaultPollInterval for tests.
	PollInterval time.Duration
}

// Worker is the embedding ingestion loop.
type Worker struct {
	deps    Deps
	stopCh  chan struct{}
	doneCh  chan struct{}
	retries map[string]int // task id → retry count
}

// New constructs a Worker. Returns nil when Deps.Embedder is nil — a
// caller can interpret nil as "embedding disabled" and skip Start.
func New(deps Deps) *Worker {
	if deps.Embedder == nil {
		return nil
	}
	if deps.PollInterval <= 0 {
		deps.PollInterval = DefaultPollInterval
	}
	return &Worker{
		deps:    deps,
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
		retries: make(map[string]int),
	}
}

// Start kicks off the worker loop in a goroutine. Returns an error when
// already started. Call Stop (or cancel the parent ctx) to terminate.
func (w *Worker) Start(parent context.Context) error {
	if w == nil {
		return nil
	}
	go w.run(parent)
	return nil
}

// Stop signals the worker to exit and waits for the goroutine to drain.
func (w *Worker) Stop() {
	if w == nil {
		return
	}
	select {
	case <-w.stopCh:
		// already stopped
	default:
		close(w.stopCh)
	}
	<-w.doneCh
}

func (w *Worker) run(parent context.Context) {
	defer close(w.doneCh)
	tick := time.NewTicker(w.deps.PollInterval)
	defer tick.Stop()
	for {
		select {
		case <-parent.Done():
			return
		case <-w.stopCh:
			return
		case <-tick.C:
			w.drainOnce(parent)
		}
	}
}

// DrainOnce processes every pending embed task in one pass. Exposed so
// tests can drive the worker synchronously without spinning the polling
// loop.
func (w *Worker) DrainOnce(ctx context.Context) {
	w.drainOnce(ctx)
}

func (w *Worker) drainOnce(ctx context.Context) {
	rows, err := w.deps.Tasks.List(ctx, task.ListOptions{
		Origin: task.OriginEvent,
		Status: task.StatusEnqueued,
	}, nil)
	if err != nil {
		w.logf("error", "list pending embed tasks", "err", err.Error())
		return
	}
	for _, t := range rows {
		select {
		case <-ctx.Done():
			return
		default:
		}
		w.handleTask(ctx, t)
	}
}

func (w *Worker) handleTask(ctx context.Context, t task.Task) {
	if docPayload, ok := document.ParseEmbedPayload(t.Payload); ok {
		w.handleDocumentEmbed(ctx, t, docPayload)
		return
	}
	if ledPayload, ok := ledger.ParseEmbedPayload(t.Payload); ok {
		w.handleLedgerEmbed(ctx, t, ledPayload)
		return
	}
	// Unknown payload kind — leave the task for whatever handler owns it.
}

func (w *Worker) handleDocumentEmbed(ctx context.Context, t task.Task, payload document.EmbedPayload) {
	doc, err := w.deps.Docs.GetByID(ctx, payload.DocumentID, []string{payload.WorkspaceID})
	if err != nil {
		w.failTask(ctx, t, payload.DocumentID, "document_lookup", err)
		return
	}
	if doc.Status != document.StatusActive {
		// Parent archived between Enqueue and the worker pass — skip.
		_, _ = w.deps.Tasks.Close(ctx, t.ID, task.OutcomeSuccess, time.Now().UTC(), nil)
		return
	}
	chunks := embeddings.Chunk(doc.Body, 0, 0)
	if len(chunks) == 0 {
		_, _ = w.deps.Tasks.Close(ctx, t.ID, task.OutcomeSuccess, time.Now().UTC(), nil)
		return
	}
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Content
	}
	vecs, err := w.deps.Embedder.Embed(ctx, texts)
	if err != nil {
		w.failTask(ctx, t, payload.DocumentID, "embed", err)
		return
	}
	if len(vecs) != len(chunks) {
		w.failTask(ctx, t, payload.DocumentID, "embed_mismatch",
			fmt.Errorf("got %d vectors for %d chunks", len(vecs), len(chunks)))
		return
	}
	now := time.Now().UTC()
	written := make([]document.Chunk, len(chunks))
	for i, c := range chunks {
		written[i] = document.Chunk{
			ID:             fmt.Sprintf("%s-c%d", doc.ID, c.Index),
			DocumentID:     doc.ID,
			WorkspaceID:    doc.WorkspaceID,
			ChunkIdx:       c.Index,
			Body:           c.Content,
			Embedding:      vecs[i],
			EmbeddingModel: w.deps.Embedder.Model(),
			CreatedAt:      now,
		}
	}
	if err := w.deps.DocChunks.Upsert(ctx, doc.ID, written); err != nil {
		w.failTask(ctx, t, payload.DocumentID, "chunk_upsert", err)
		return
	}
	if _, err := w.deps.Tasks.Close(ctx, t.ID, task.OutcomeSuccess, now, nil); err != nil {
		w.logf("warn", "close embed task after success failed", "task_id", t.ID, "err", err.Error())
	}
}

func (w *Worker) handleLedgerEmbed(ctx context.Context, t task.Task, payload ledger.EmbedPayload) {
	row, err := w.deps.Ledger.GetByID(ctx, payload.LedgerID, []string{payload.WorkspaceID})
	if err != nil {
		w.failTask(ctx, t, payload.LedgerID, "ledger_lookup", err)
		return
	}
	if row.Status != ledger.StatusActive {
		// Dereferenced or archived between Enqueue and worker pass — skip.
		_, _ = w.deps.Tasks.Close(ctx, t.ID, task.OutcomeSuccess, time.Now().UTC(), nil)
		return
	}
	chunks := embeddings.Chunk(row.Content, 0, 0)
	if len(chunks) == 0 {
		_, _ = w.deps.Tasks.Close(ctx, t.ID, task.OutcomeSuccess, time.Now().UTC(), nil)
		return
	}
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Content
	}
	vecs, err := w.deps.Embedder.Embed(ctx, texts)
	if err != nil {
		w.failTask(ctx, t, payload.LedgerID, "embed", err)
		return
	}
	if len(vecs) != len(chunks) {
		w.failTask(ctx, t, payload.LedgerID, "embed_mismatch",
			fmt.Errorf("got %d vectors for %d chunks", len(vecs), len(chunks)))
		return
	}
	now := time.Now().UTC()
	written := make([]ledger.Chunk, len(chunks))
	for i, c := range chunks {
		written[i] = ledger.Chunk{
			ID:             fmt.Sprintf("%s-c%d", row.ID, c.Index),
			LedgerID:       row.ID,
			WorkspaceID:    row.WorkspaceID,
			ChunkIdx:       c.Index,
			Body:           c.Content,
			Embedding:      vecs[i],
			EmbeddingModel: w.deps.Embedder.Model(),
			CreatedAt:      now,
		}
	}
	if err := w.deps.LedgerChunks.Upsert(ctx, row.ID, written); err != nil {
		w.failTask(ctx, t, payload.LedgerID, "chunk_upsert", err)
		return
	}
	if _, err := w.deps.Tasks.Close(ctx, t.ID, task.OutcomeSuccess, now, nil); err != nil {
		w.logf("warn", "close embed task after success failed", "task_id", t.ID, "err", err.Error())
	}
}

// failTask is the centralised failure path. We track retry counts in
// the worker's own map (the Task surface's Reclaim mechanism requires
// status=claimed which our List+process model bypasses for simplicity).
// On retry exhaustion we write a kind:embedding-failure ledger row and
// Close the task with outcome=failure; otherwise we leave the task in
// status=enqueued so the next drainOnce pass picks it up again.
func (w *Worker) failTask(ctx context.Context, t task.Task, sourceID, stage string, cause error) {
	w.retries[t.ID]++
	if w.retries[t.ID] > MaxRetries {
		now := time.Now().UTC()
		w.writeFailureLedger(ctx, t, sourceID, stage, cause)
		if _, err := w.deps.Tasks.Close(ctx, t.ID, task.OutcomeFailure, now, nil); err != nil {
			w.logf("warn", "close failed embed task", "task_id", t.ID, "err", err.Error())
		}
		delete(w.retries, t.ID)
		return
	}
	w.logf("warn", "embed task transient failure", "task_id", t.ID, "stage", stage, "err", cause.Error())
}

// writeFailureLedger records a kind:embedding-failure row carrying the
// parent primitive id and the provider error so the failure is auditable
// on the ledger.
func (w *Worker) writeFailureLedger(ctx context.Context, t task.Task, sourceID, stage string, cause error) {
	if w.deps.Ledger == nil {
		return
	}
	structured, _ := json.Marshal(map[string]any{
		"task_id":   t.ID,
		"source_id": sourceID,
		"stage":     stage,
		"error":     cause.Error(),
	})
	entry := ledger.LedgerEntry{
		WorkspaceID: t.WorkspaceID,
		ProjectID:   t.ProjectID,
		Type:        ledger.TypeEvidence,
		Tags:        []string{"kind:embedding-failure", "source:" + sourceID},
		Content:     fmt.Sprintf("embedding failed for %s at stage %s: %v", sourceID, stage, cause),
		Structured:  structured,
		Durability:  ledger.DurabilityDurable,
		SourceType:  ledger.SourceSystem,
		Status:      ledger.StatusActive,
		CreatedBy:   "system:embedworker",
	}
	if _, err := w.deps.Ledger.Append(ctx, entry, time.Now().UTC()); err != nil {
		w.logf("warn", "write embedding-failure ledger row", "err", err.Error())
	}
}

// logf emits a structured log line via the configured arbor logger.
// Drops on a nil logger so tests can pass nil. Caller passes pairs of
// key/value strings; we route to arbor's Str chains for the matching
// level. Unknown levels fall back to Info.
func (w *Worker) logf(level, msg string, kv ...string) {
	if w.deps.Logger == nil {
		return
	}
	var ev = w.deps.Logger.Info()
	switch level {
	case "warn":
		ev = w.deps.Logger.Warn()
	case "error":
		ev = w.deps.Logger.Error()
	}
	for i := 0; i+1 < len(kv); i += 2 {
		ev = ev.Str(kv[i], kv[i+1])
	}
	ev.Msg(msg)
}

// ErrEmbedderUnavailable is returned by helpers that require a non-nil
// embedder but were called against a worker constructed without one.
var ErrEmbedderUnavailable = errors.New("embedworker: embedder not configured")
