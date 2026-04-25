package embedworker

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/embeddings"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/task"
)

func TestWorker_DocumentHappyPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Now().UTC()

	docChunks := document.NewMemoryChunkStore()
	stub := embeddings.NewStubEmbedder(0)
	docs := document.NewMemoryStoreWithEmbeddings(stub, docChunks)
	led := ledger.NewMemoryStore()
	tasks := task.NewMemoryStore()

	doc, err := docs.Create(ctx, document.Document{
		Type:        document.TypePrinciple,
		Scope:       document.ScopeSystem,
		Name:        "alpha",
		Body:        "this is enough body content to exceed the noise threshold for embedding",
		WorkspaceID: "ws_1",
	}, now)
	if err != nil {
		t.Fatalf("create doc: %v", err)
	}
	if _, err := document.EnqueueIngest(ctx, tasks, doc, now); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	w := New(Deps{
		Tasks: tasks, Embedder: stub, Docs: docs, DocChunks: docChunks,
		Ledger: led, LedgerChunks: ledger.NewMemoryChunkStore(),
		PollInterval: time.Millisecond,
	})
	w.DrainOnce(ctx)

	// Chunks are in the store.
	hits, _ := docChunks.SearchByEmbedding(ctx, document.ChunkSearchOptions{
		Embedding: mustEmbed(t, stub, "this is enough body content"), TopK: 5,
	}, []string{doc.WorkspaceID})
	if len(hits) == 0 {
		t.Fatalf("no chunks written for doc %s", doc.ID)
	}

	// Task is closed with outcome=success.
	tasksList, _ := tasks.List(ctx, task.ListOptions{Status: task.StatusClosed}, nil)
	if len(tasksList) != 1 || tasksList[0].Outcome != task.OutcomeSuccess {
		t.Fatalf("expected one closed task with outcome=success, got %+v", tasksList)
	}
}

func TestWorker_LedgerHappyPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Now().UTC()

	ledChunks := ledger.NewMemoryChunkStore()
	stub := embeddings.NewStubEmbedder(0)
	led := ledger.NewMemoryStoreWithEmbeddings(stub, ledChunks)
	docs := document.NewMemoryStore()
	tasks := task.NewMemoryStore()

	row, err := led.Append(ctx, ledger.LedgerEntry{
		WorkspaceID: "ws_1",
		ProjectID:   "proj_a",
		Type:        ledger.TypeEvidence,
		Content:     "this evidence row carries enough body to be embed-worthy via the worker",
	}, now)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := ledger.EnqueueAppend(ctx, tasks, row, now); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	w := New(Deps{
		Tasks: tasks, Embedder: stub,
		Docs: docs, DocChunks: document.NewMemoryChunkStore(),
		Ledger: led, LedgerChunks: ledChunks,
	})
	w.DrainOnce(ctx)

	hits, _ := ledChunks.SearchByEmbedding(ctx, ledger.ChunkSearchOptions{
		Embedding: mustEmbed(t, stub, "this evidence row carries enough body"), TopK: 5,
	}, []string{row.WorkspaceID})
	if len(hits) == 0 {
		t.Fatalf("no chunks written for ledger row %s", row.ID)
	}

	tasksList, _ := tasks.List(ctx, task.ListOptions{Status: task.StatusClosed}, nil)
	if len(tasksList) != 1 || tasksList[0].Outcome != task.OutcomeSuccess {
		t.Fatalf("expected one closed task with outcome=success, got %+v", tasksList)
	}
}

// failingEmbedder always errors so we can drive the worker's retry path.
type failingEmbedder struct{ dim int }

func (f *failingEmbedder) Embed(_ context.Context, _ []string) ([][]float32, error) {
	return nil, errors.New("simulated provider failure")
}
func (f *failingEmbedder) Dimension() int { return f.dim }
func (f *failingEmbedder) Model() string  { return "fail" }

func TestWorker_RetryExhaustionWritesLedgerRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Now().UTC()

	docChunks := document.NewMemoryChunkStore()
	docs := document.NewMemoryStoreWithEmbeddings(&failingEmbedder{dim: 4}, docChunks)
	led := ledger.NewMemoryStore()
	tasks := task.NewMemoryStore()

	doc, err := docs.Create(ctx, document.Document{
		Type:        document.TypePrinciple,
		Scope:       document.ScopeSystem,
		Name:        "alpha",
		Body:        "this is enough body content to exceed the noise threshold for embedding",
		WorkspaceID: "ws_1",
	}, now)
	if err != nil {
		t.Fatalf("create doc: %v", err)
	}
	enqueued, err := document.EnqueueIngest(ctx, tasks, doc, now)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	w := New(Deps{
		Tasks: tasks, Embedder: &failingEmbedder{dim: 4},
		Docs: docs, DocChunks: docChunks,
		Ledger: led, LedgerChunks: ledger.NewMemoryChunkStore(),
	})

	// Drain N+1 times — each pass picks the still-enqueued task, fails,
	// reclaims (incrementing ReclaimCount). On pass MaxRetries+1 the
	// worker writes the failure ledger row and closes.
	for i := 0; i <= MaxRetries+1; i++ {
		w.DrainOnce(ctx)
	}

	// Task should be closed with outcome=failure.
	closed, _ := tasks.GetByID(ctx, enqueued.ID, nil)
	if closed.Status != task.StatusClosed {
		t.Fatalf("task status = %s, want closed (after %d retries)", closed.Status, MaxRetries)
	}
	if closed.Outcome != task.OutcomeFailure {
		t.Fatalf("task outcome = %s, want failure", closed.Outcome)
	}

	// A kind:embedding-failure ledger row has been written.
	rows, _ := led.List(ctx, "", ledger.ListOptions{}, nil)
	var found bool
	for _, r := range rows {
		for _, tag := range r.Tags {
			if tag == "kind:embedding-failure" {
				found = true
				if !strings.Contains(r.Content, doc.ID) {
					t.Errorf("failure row missing doc id; content=%q", r.Content)
				}
				break
			}
		}
	}
	if !found {
		t.Fatalf("no kind:embedding-failure ledger row found")
	}
}

func TestWorker_NilEmbedderReturnsNil(t *testing.T) {
	t.Parallel()
	if w := New(Deps{}); w != nil {
		t.Errorf("expected nil worker when embedder is nil, got %v", w)
	}
}

func mustEmbed(t *testing.T, e embeddings.Embedder, text string) []float32 {
	t.Helper()
	vecs, err := e.Embed(context.Background(), []string{text})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	return vecs[0]
}
