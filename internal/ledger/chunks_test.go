package ledger

import (
	"context"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/embeddings"
)

func TestMemoryChunkStore_UpsertAndSearch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cs := NewMemoryChunkStore()
	chunks := []Chunk{
		{ID: "c1", LedgerID: "ldg_a", WorkspaceID: "ws_1", ChunkIdx: 0, Body: "alpha", Embedding: []float32{1, 0, 0}, EmbeddingModel: "stub", CreatedAt: time.Now()},
		{ID: "c2", LedgerID: "ldg_a", WorkspaceID: "ws_1", ChunkIdx: 1, Body: "beta", Embedding: []float32{0, 1, 0}, EmbeddingModel: "stub", CreatedAt: time.Now()},
	}
	if err := cs.Upsert(ctx, "ldg_a", chunks); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	hits, err := cs.SearchByEmbedding(ctx, ChunkSearchOptions{Embedding: []float32{1, 0, 0}, TopK: 5}, []string{"ws_1"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits=%d, want 2", len(hits))
	}
	if hits[0].ID != "c1" {
		t.Errorf("top hit = %s, want c1", hits[0].ID)
	}
}

func TestMemoryChunkStore_DeleteByLedgerID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cs := NewMemoryChunkStore()
	_ = cs.Upsert(ctx, "a", []Chunk{{ID: "x", LedgerID: "a", WorkspaceID: "ws", Embedding: []float32{1, 0}}})
	_ = cs.Upsert(ctx, "b", []Chunk{{ID: "y", LedgerID: "b", WorkspaceID: "ws", Embedding: []float32{0, 1}}})
	if err := cs.DeleteByLedgerID(ctx, "a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	hits, _ := cs.SearchByEmbedding(ctx, ChunkSearchOptions{Embedding: []float32{1, 0}, TopK: 5}, []string{"ws"})
	for _, h := range hits {
		if h.LedgerID == "a" {
			t.Errorf("ldg_a chunk %s leaked after delete", h.ID)
		}
	}
}

func TestMemoryChunkStore_WorkspaceIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cs := NewMemoryChunkStore()
	_ = cs.Upsert(ctx, "a", []Chunk{{ID: "x", LedgerID: "a", WorkspaceID: "visible", Embedding: []float32{1, 0}}})
	_ = cs.Upsert(ctx, "b", []Chunk{{ID: "y", LedgerID: "b", WorkspaceID: "hidden", Embedding: []float32{1, 0}}})
	hits, _ := cs.SearchByEmbedding(ctx, ChunkSearchOptions{Embedding: []float32{1, 0}, TopK: 5}, []string{"visible"})
	for _, h := range hits {
		if h.WorkspaceID != "visible" {
			t.Errorf("hit from %s leaked through membership filter", h.WorkspaceID)
		}
	}
}

func TestMemoryStore_SearchSemantic_NoEmbedder(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	_, err := store.SearchSemantic(context.Background(), "proj_a", "q", SearchOptions{}, nil)
	if err != ErrSemanticUnavailable {
		t.Fatalf("err=%v, want ErrSemanticUnavailable", err)
	}
}

func TestMemoryStore_SearchSemantic_RanksRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cs := NewMemoryChunkStore()
	stub := embeddings.NewStubEmbedder(0)
	store := NewMemoryStoreWithEmbeddings(stub, cs)

	now := time.Now().UTC()
	row1, _ := store.Append(ctx, LedgerEntry{
		ProjectID: "proj_a", Type: TypeEvidence,
		Content: "agile delivery scope reduction principle",
	}, now)
	row2, _ := store.Append(ctx, LedgerEntry{
		ProjectID: "proj_a", Type: TypeEvidence,
		Content: "completely unrelated cosmic ray content",
	}, now.Add(time.Second))

	for _, r := range []LedgerEntry{row1, row2} {
		vecs, _ := stub.Embed(ctx, []string{r.Content})
		_ = cs.Upsert(ctx, r.ID, []Chunk{{
			ID: r.ID + "-c0", LedgerID: r.ID, WorkspaceID: r.WorkspaceID,
			Body: r.Content, Embedding: vecs[0], EmbeddingModel: stub.Model(),
			ChunkIdx: 0, CreatedAt: now,
		}})
	}

	got, err := store.SearchSemantic(ctx, "proj_a", "agile delivery scope reduction principle", SearchOptions{TopK: 5}, nil)
	if err != nil {
		t.Fatalf("SearchSemantic: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2", len(got))
	}
	if got[0].ID != row1.ID {
		t.Errorf("top row = %s, want %s (matches query)", got[0].ID, row1.ID)
	}
	if got[0].BestChunkScore == nil || *got[0].BestChunkScore < 0.99 {
		t.Errorf("top row BestChunkScore = %v, want ~1.0", got[0].BestChunkScore)
	}
}

func TestMemoryStore_DereferenceCascadesChunks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cs := NewMemoryChunkStore()
	stub := embeddings.NewStubEmbedder(0)
	store := NewMemoryStoreWithEmbeddings(stub, cs)

	now := time.Now().UTC()
	row, _ := store.Append(ctx, LedgerEntry{
		ProjectID: "proj_a", Type: TypeEvidence, Content: "to be dereferenced",
	}, now)
	vecs, _ := stub.Embed(ctx, []string{row.Content})
	_ = cs.Upsert(ctx, row.ID, []Chunk{{
		ID: row.ID + "-c0", LedgerID: row.ID, WorkspaceID: row.WorkspaceID,
		Body: row.Content, Embedding: vecs[0], EmbeddingModel: stub.Model(),
		ChunkIdx: 0, CreatedAt: now,
	}})

	// Sanity: chunk is visible.
	hits, _ := cs.SearchByEmbedding(ctx, ChunkSearchOptions{Embedding: vecs[0], TopK: 5}, nil)
	if len(hits) != 1 {
		t.Fatalf("pre-deref hits=%d, want 1", len(hits))
	}

	if _, err := store.Dereference(ctx, row.ID, "noisy-row", "tester", now.Add(time.Hour), nil); err != nil {
		t.Fatalf("dereference: %v", err)
	}

	// Post-cascade: chunk store no longer carries the row's chunks.
	hits, _ = cs.SearchByEmbedding(ctx, ChunkSearchOptions{Embedding: vecs[0], TopK: 5}, nil)
	for _, h := range hits {
		if h.LedgerID == row.ID {
			t.Errorf("dereferenced row %s leaked chunk %s after cascade", row.ID, h.ID)
		}
	}

	// SearchSemantic also filters out dereferenced rows even if the
	// cascade somehow missed.
	got, _ := store.SearchSemantic(ctx, "proj_a", row.Content, SearchOptions{
		ListOptions: ListOptions{IncludeDerefd: true},
		TopK:        5,
	}, nil)
	for _, g := range got {
		if g.ID == row.ID {
			t.Errorf("dereferenced row %s present in SearchSemantic results", row.ID)
		}
	}
}
