package ledger

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/bobmcallan/satellites/internal/embeddings"
)

// ErrSemanticUnavailable is returned by SearchSemantic when the store was
// constructed without an Embedder + ChunkStore.
var ErrSemanticUnavailable = errors.New("ledger: semantic search unavailable (no embedder configured)")

// Chunk is a single embedded chunk of a ledger row's Content. Structured
// JSON payloads are deliberately not chunked — they're filterable via
// tags + JSON fields and embedding them would dilute the signal.
type Chunk struct {
	ID             string
	LedgerID       string
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

// ChunkSearchOptions parameterises a SearchByEmbedding call. RestrictLedgerIDs
// is the optional pre-filter the caller computed from structured filters.
type ChunkSearchOptions struct {
	Embedding         []float32
	TopK              int
	RestrictLedgerIDs []string
}

const defaultChunkTopK = 32

// ChunkStore is the persistence surface for ledger chunks.
type ChunkStore interface {
	// Upsert replaces every chunk for ledgerID with the supplied set.
	Upsert(ctx context.Context, ledgerID string, chunks []Chunk) error

	// DeleteByLedgerID removes every chunk row for ledgerID. Called by
	// Dereference so dereferenced rows vanish from semantic search results.
	DeleteByLedgerID(ctx context.Context, ledgerID string) error

	// SearchByEmbedding returns the top-K chunks (by cosine similarity)
	// scoped to memberships and (optionally) the RestrictLedgerIDs
	// pre-filter, in score-descending order.
	SearchByEmbedding(ctx context.Context, opts ChunkSearchOptions, memberships []string) ([]ChunkHit, error)
}

// MemoryChunkStore is the in-process ChunkStore.
type MemoryChunkStore struct {
	mu       sync.Mutex
	byLedger map[string][]Chunk
}

// NewMemoryChunkStore returns an empty store.
func NewMemoryChunkStore() *MemoryChunkStore {
	return &MemoryChunkStore{byLedger: make(map[string][]Chunk)}
}

// Upsert implements ChunkStore.
func (m *MemoryChunkStore) Upsert(_ context.Context, ledgerID string, chunks []Chunk) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(chunks) == 0 {
		delete(m.byLedger, ledgerID)
		return nil
	}
	cp := make([]Chunk, len(chunks))
	copy(cp, chunks)
	m.byLedger[ledgerID] = cp
	return nil
}

// DeleteByLedgerID implements ChunkStore.
func (m *MemoryChunkStore) DeleteByLedgerID(_ context.Context, ledgerID string) error {
	m.mu.Lock()
	delete(m.byLedger, ledgerID)
	m.mu.Unlock()
	return nil
}

// SearchByEmbedding implements ChunkStore. Brute-force cosine over every
// workspace-visible chunk; pre-filter by parent IDs to keep N manageable.
func (m *MemoryChunkStore) SearchByEmbedding(_ context.Context, opts ChunkSearchOptions, memberships []string) ([]ChunkHit, error) {
	if len(opts.Embedding) == 0 {
		return nil, errors.New("ledger: SearchByEmbedding requires an Embedding")
	}
	topK := opts.TopK
	if topK <= 0 {
		topK = defaultChunkTopK
	}

	var allow map[string]struct{}
	if len(opts.RestrictLedgerIDs) > 0 {
		allow = make(map[string]struct{}, len(opts.RestrictLedgerIDs))
		for _, id := range opts.RestrictLedgerIDs {
			allow[id] = struct{}{}
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	hits := make([]ChunkHit, 0)
	for ledID, chunks := range m.byLedger {
		if allow != nil {
			if _, ok := allow[ledID]; !ok {
				continue
			}
		}
		for _, c := range chunks {
			if memberships != nil && !inLedgerMemberships(c.WorkspaceID, memberships) {
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
