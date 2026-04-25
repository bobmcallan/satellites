package document

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/bobmcallan/satellites/internal/embeddings"
)

// ErrSemanticUnavailable is returned by SearchSemantic when the store was
// constructed without an Embedder + ChunkStore — the deploy hasn't opted
// in to semantic search. Callers fall back to the structured-filter path.
var ErrSemanticUnavailable = errors.New("document: semantic search unavailable (no embedder configured)")

// Chunk is a single embedded chunk of a document. The store keeps these
// alongside the parent row; cosine ranking happens in process.
type Chunk struct {
	ID             string
	DocumentID     string
	WorkspaceID    string
	ChunkIdx       int
	Body           string
	Embedding      []float32
	EmbeddingModel string
	CreatedAt      time.Time
}

// ChunkHit is a chunk plus its cosine score against a query embedding.
type ChunkHit struct {
	Chunk
	Score float32
}

// ChunkSearchOptions parameterises a SearchByEmbedding call. Embedding is
// required; TopK ≤0 falls back to a sensible default. RestrictDocumentIDs
// is the optional pre-filter the caller computed from structured filters
// (Type/Scope/Tags/...) — when non-empty, only chunks whose DocumentID is
// in the slice are scored.
type ChunkSearchOptions struct {
	Embedding           []float32
	TopK                int
	RestrictDocumentIDs []string
}

const defaultChunkTopK = 32

// ChunkStore is the persistence surface for document chunks. SurrealStore
// is the production impl; MemoryChunkStore is the in-process test double.
type ChunkStore interface {
	// Upsert replaces every chunk for documentID with the supplied set.
	// Atomic per documentID — partial failures must not leave stale rows.
	Upsert(ctx context.Context, documentID string, chunks []Chunk) error

	// DeleteByDocumentID removes every chunk row for documentID. Used on
	// document hard-delete and on re-ingest before Upsert.
	DeleteByDocumentID(ctx context.Context, documentID string) error

	// SearchByEmbedding returns the top-K chunks (by cosine similarity to
	// opts.Embedding) whose WorkspaceID is in memberships and — when
	// opts.RestrictDocumentIDs is non-empty — whose DocumentID is in the
	// pre-filter list. Returned in score order (descending).
	SearchByEmbedding(ctx context.Context, opts ChunkSearchOptions, memberships []string) ([]ChunkHit, error)
}

// MemoryChunkStore is the in-process ChunkStore used by unit tests.
type MemoryChunkStore struct {
	mu    sync.Mutex
	byDoc map[string][]Chunk
}

// NewMemoryChunkStore returns an empty store.
func NewMemoryChunkStore() *MemoryChunkStore {
	return &MemoryChunkStore{byDoc: make(map[string][]Chunk)}
}

// Upsert implements ChunkStore.
func (m *MemoryChunkStore) Upsert(_ context.Context, documentID string, chunks []Chunk) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(chunks) == 0 {
		delete(m.byDoc, documentID)
		return nil
	}
	cp := make([]Chunk, len(chunks))
	copy(cp, chunks)
	m.byDoc[documentID] = cp
	return nil
}

// DeleteByDocumentID implements ChunkStore.
func (m *MemoryChunkStore) DeleteByDocumentID(_ context.Context, documentID string) error {
	m.mu.Lock()
	delete(m.byDoc, documentID)
	m.mu.Unlock()
	return nil
}

// SearchByEmbedding implements ChunkStore. Brute-force cosine over every
// workspace-visible chunk. Linear in chunk count; pre-filter by parent
// IDs to keep N manageable.
func (m *MemoryChunkStore) SearchByEmbedding(_ context.Context, opts ChunkSearchOptions, memberships []string) ([]ChunkHit, error) {
	if len(opts.Embedding) == 0 {
		return nil, errors.New("document: SearchByEmbedding requires an Embedding")
	}
	topK := opts.TopK
	if topK <= 0 {
		topK = defaultChunkTopK
	}

	var allow map[string]struct{}
	if len(opts.RestrictDocumentIDs) > 0 {
		allow = make(map[string]struct{}, len(opts.RestrictDocumentIDs))
		for _, id := range opts.RestrictDocumentIDs {
			allow[id] = struct{}{}
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	hits := make([]ChunkHit, 0)
	for docID, chunks := range m.byDoc {
		if allow != nil {
			if _, ok := allow[docID]; !ok {
				continue
			}
		}
		for _, c := range chunks {
			if memberships != nil && !inDocMemberships(c.WorkspaceID, memberships) {
				continue
			}
			score, err := embeddings.Cosine(opts.Embedding, c.Embedding)
			if err != nil {
				continue
			}
			hits = append(hits, ChunkHit{Chunk: c, Score: score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > topK {
		hits = hits[:topK]
	}
	return hits, nil
}
