package document

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
		{ID: "chk_1", DocumentID: "doc_a", WorkspaceID: "ws_1", ChunkIdx: 0, Body: "alpha", Embedding: []float32{1, 0, 0}, EmbeddingModel: "stub", CreatedAt: time.Now()},
		{ID: "chk_2", DocumentID: "doc_a", WorkspaceID: "ws_1", ChunkIdx: 1, Body: "beta", Embedding: []float32{0, 1, 0}, EmbeddingModel: "stub", CreatedAt: time.Now()},
	}
	if err := cs.Upsert(ctx, "doc_a", chunks); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	hits, err := cs.SearchByEmbedding(ctx, ChunkSearchOptions{
		Embedding: []float32{1, 0, 0},
		TopK:      5,
	}, []string{"ws_1"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits=%d, want 2", len(hits))
	}
	if hits[0].ID != "chk_1" {
		t.Errorf("top hit = %s, want chk_1 (matches query)", hits[0].ID)
	}
	if hits[0].Score <= hits[1].Score {
		t.Errorf("top score %v <= second %v; expected strict descending", hits[0].Score, hits[1].Score)
	}
}

func TestMemoryChunkStore_DeleteByDocumentID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cs := NewMemoryChunkStore()
	_ = cs.Upsert(ctx, "doc_a", []Chunk{{ID: "x", DocumentID: "doc_a", WorkspaceID: "ws", Embedding: []float32{1, 0}}})
	_ = cs.Upsert(ctx, "doc_b", []Chunk{{ID: "y", DocumentID: "doc_b", WorkspaceID: "ws", Embedding: []float32{0, 1}}})

	if err := cs.DeleteByDocumentID(ctx, "doc_a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	hits, _ := cs.SearchByEmbedding(ctx, ChunkSearchOptions{Embedding: []float32{1, 0}, TopK: 5}, []string{"ws"})
	for _, h := range hits {
		if h.DocumentID == "doc_a" {
			t.Errorf("doc_a chunk %s leaked after delete", h.ID)
		}
	}
}

func TestMemoryChunkStore_WorkspaceIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cs := NewMemoryChunkStore()
	_ = cs.Upsert(ctx, "doc_a", []Chunk{{ID: "x", DocumentID: "doc_a", WorkspaceID: "ws_visible", Embedding: []float32{1, 0}}})
	_ = cs.Upsert(ctx, "doc_b", []Chunk{{ID: "y", DocumentID: "doc_b", WorkspaceID: "ws_hidden", Embedding: []float32{1, 0}}})

	hits, _ := cs.SearchByEmbedding(ctx, ChunkSearchOptions{Embedding: []float32{1, 0}, TopK: 5}, []string{"ws_visible"})
	for _, h := range hits {
		if h.WorkspaceID != "ws_visible" {
			t.Errorf("hit from %s leaked through membership filter", h.WorkspaceID)
		}
	}
}

func TestMemoryChunkStore_RestrictDocumentIDs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cs := NewMemoryChunkStore()
	_ = cs.Upsert(ctx, "doc_a", []Chunk{{ID: "x", DocumentID: "doc_a", WorkspaceID: "ws", Embedding: []float32{1, 0}}})
	_ = cs.Upsert(ctx, "doc_b", []Chunk{{ID: "y", DocumentID: "doc_b", WorkspaceID: "ws", Embedding: []float32{1, 0}}})

	hits, _ := cs.SearchByEmbedding(ctx, ChunkSearchOptions{
		Embedding:           []float32{1, 0},
		TopK:                5,
		RestrictDocumentIDs: []string{"doc_a"},
	}, []string{"ws"})
	if len(hits) != 1 || hits[0].DocumentID != "doc_a" {
		t.Errorf("hits = %+v, want [doc_a]", hits)
	}
}

func TestMemoryStore_SearchSemantic_NoEmbedder(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore() // no embedder
	_, err := store.SearchSemantic(context.Background(), "q", SearchOptions{}, nil)
	if err != ErrSemanticUnavailable {
		t.Fatalf("err = %v, want ErrSemanticUnavailable", err)
	}
}

func TestMemoryStore_SearchSemantic_RanksDocs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cs := NewMemoryChunkStore()
	stub := embeddings.NewStubEmbedder(0)
	store := NewMemoryStoreWithEmbeddings(stub, cs)

	now := time.Now()
	doc1, _ := store.Create(ctx, Document{
		Type: TypePrinciple, Scope: ScopeSystem, Name: "alpha",
		Body: "agile delivery scope reduction",
	}, now)
	doc2, _ := store.Create(ctx, Document{
		Type: TypePrinciple, Scope: ScopeSystem, Name: "beta",
		Body: "completely unrelated cosmic ray content",
	}, now)

	// Chunk + embed each document body, write into the chunk store. In
	// production this is the worker's job (C4); here we do it inline so
	// we can prove SearchSemantic returns ranked results.
	for _, d := range []Document{doc1, doc2} {
		texts := []string{d.Body}
		vecs, _ := stub.Embed(ctx, texts)
		_ = cs.Upsert(ctx, d.ID, []Chunk{{
			ID:             d.ID + "-c0",
			DocumentID:     d.ID,
			WorkspaceID:    d.WorkspaceID,
			Body:           d.Body,
			Embedding:      vecs[0],
			EmbeddingModel: stub.Model(),
			ChunkIdx:       0,
			CreatedAt:      now,
		}})
	}

	// Stub embedder is deterministic — querying the exact body of doc1
	// produces a vector equal to doc1's chunk vector → cosine = 1, and
	// doc1 ranks first.
	got, err := store.SearchSemantic(ctx, "agile delivery scope reduction", SearchOptions{TopK: 5}, nil)
	if err != nil {
		t.Fatalf("SearchSemantic: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d docs, want 2", len(got))
	}
	if got[0].ID != doc1.ID {
		t.Errorf("top doc = %s, want %s (alpha — matches query)", got[0].ID, doc1.ID)
	}
	if got[0].BestChunkScore == nil || *got[0].BestChunkScore < 0.99 {
		t.Errorf("top doc BestChunkScore = %v, want ~1.0", got[0].BestChunkScore)
	}
}

func TestMemoryStore_SearchSemantic_PreFilter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cs := NewMemoryChunkStore()
	stub := embeddings.NewStubEmbedder(0)
	store := NewMemoryStoreWithEmbeddings(stub, cs)

	now := time.Now()
	docPrinciple, _ := store.Create(ctx, Document{
		Type: TypePrinciple, Scope: ScopeSystem, Name: "p", Body: "shared body",
	}, now)
	docContract, _ := store.Create(ctx, Document{
		Type: TypeContract, Scope: ScopeSystem, Name: "c", Body: "shared body",
	}, now)

	for _, d := range []Document{docPrinciple, docContract} {
		vecs, _ := stub.Embed(ctx, []string{d.Body})
		_ = cs.Upsert(ctx, d.ID, []Chunk{{
			ID: d.ID + "-c0", DocumentID: d.ID, WorkspaceID: d.WorkspaceID,
			Body: d.Body, Embedding: vecs[0], EmbeddingModel: stub.Model(),
			ChunkIdx: 0, CreatedAt: now,
		}})
	}

	got, err := store.SearchSemantic(ctx, "shared body", SearchOptions{
		ListOptions: ListOptions{Type: TypePrinciple},
		TopK:        5,
	}, nil)
	if err != nil {
		t.Fatalf("SearchSemantic: %v", err)
	}
	if len(got) != 1 || got[0].Type != TypePrinciple {
		t.Fatalf("structured filter not pre-applied: %+v", got)
	}
}
