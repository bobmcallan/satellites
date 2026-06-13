package embed

import (
	"math"
	"testing"
)

func TestChunker_Window(t *testing.T) {
	// words=7, window=4, overlap=1 → advance=3: chunks start at 0 then 3.
	// [0:4] one two three four ; [3:7] four five six seven (overlap "four").
	got := Chunker{MaxTokens: 4, Overlap: 1}.Chunk("one two three four five six seven")
	if len(got) != 2 {
		t.Fatalf("chunks = %d, want 2: %+v", len(got), got)
	}
	if got[0].Content != "one two three four" || got[1].Content != "four five six seven" {
		t.Errorf("chunk windows wrong: %q / %q", got[0].Content, got[1].Content)
	}
	if got[0].Index != 0 || got[1].Index != 1 {
		t.Errorf("indices = %d,%d want 0,1", got[0].Index, got[1].Index)
	}
	if got[0].TokenCount != 4 {
		t.Errorf("token count = %d, want 4", got[0].TokenCount)
	}
	if c := (Chunker{}).Chunk("  "); c != nil {
		t.Errorf("blank text → nil, got %+v", c)
	}
}

func TestCosineSimilarity(t *testing.T) {
	cases := []struct {
		a, b []float64
		want float64
	}{
		{[]float64{1, 0}, []float64{1, 0}, 1},
		{[]float64{1, 0}, []float64{0, 1}, 0},
		{[]float64{1, 1}, []float64{}, 0},       // length mismatch
		{[]float64{0, 0}, []float64{1, 1}, 0},   // zero vector
		{[]float64{1, 0}, []float64{-1, 0}, -1}, // opposite
	}
	for _, c := range cases {
		if s := cosineSimilarity(c.a, c.b); math.Abs(s-c.want) > 1e-9 {
			t.Errorf("cosine(%v,%v) = %v, want %v", c.a, c.b, s, c.want)
		}
	}
}

func TestTopK_Ranks(t *testing.T) {
	q := []float64{1, 0}
	chunks := []ScoredChunk{
		{DocumentID: "a", Embedding: []float64{0, 1}},     // orthogonal → 0
		{DocumentID: "b", Embedding: []float64{1, 0}},     // identical → 1
		{DocumentID: "c", Embedding: []float64{0.7, 0.7}}, // ~0.707
	}
	got := topK(q, chunks, 2)
	if len(got) != 2 {
		t.Fatalf("topK len = %d, want 2", len(got))
	}
	if got[0].DocumentID != "b" || got[1].DocumentID != "c" {
		t.Errorf("ranking = %s,%s want b,c", got[0].DocumentID, got[1].DocumentID)
	}
}
