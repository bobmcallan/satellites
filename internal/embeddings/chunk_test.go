package embeddings

import (
	"strings"
	"testing"
)

func TestChunk_Empty(t *testing.T) {
	t.Parallel()
	if got := Chunk("", 0, 0); len(got) != 0 {
		t.Fatalf("expected 0 chunks, got %d", len(got))
	}
}

func TestChunk_WhitespaceOnly(t *testing.T) {
	t.Parallel()
	if got := Chunk("   \t\n  ", 0, 0); len(got) != 0 {
		t.Fatalf("expected 0 chunks, got %d", len(got))
	}
}

func TestChunk_SingleWord(t *testing.T) {
	t.Parallel()
	got := Chunk("hello", 0, 0)
	if len(got) != 1 {
		t.Fatalf("got %d chunks, want 1", len(got))
	}
	if got[0].Content != "hello" {
		t.Errorf("content = %q, want hello", got[0].Content)
	}
	if got[0].TokenCount != 1 {
		t.Errorf("tokens = %d, want 1", got[0].TokenCount)
	}
	if got[0].Index != 0 {
		t.Errorf("index = %d, want 0", got[0].Index)
	}
}

func TestChunk_ShortText_SingleChunk(t *testing.T) {
	t.Parallel()
	text := "the quick brown fox jumps over the lazy dog"
	got := Chunk(text, 0, 0)
	if len(got) != 1 {
		t.Fatalf("got %d chunks, want 1", len(got))
	}
	if got[0].Content != text {
		t.Errorf("content mismatch: %q", got[0].Content)
	}
}

func TestChunk_OverlapBoundaries(t *testing.T) {
	t.Parallel()
	words := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	text := strings.Join(words, " ")
	got := Chunk(text, 5, 2)

	// stride = 5 - 2 = 3 → starts at 0, 3, 6. The third chunk hits the
	// end of the input (words[6:10] = "g h i j") so the loop breaks.
	wantStarts := []string{"a b c d e", "d e f g h", "g h i j"}
	if len(got) != len(wantStarts) {
		t.Fatalf("got %d chunks, want %d (%v)", len(got), len(wantStarts), got)
	}
	for i, want := range wantStarts {
		if got[i].Content != want {
			t.Errorf("chunk %d = %q, want %q", i, got[i].Content, want)
		}
		if got[i].Index != i {
			t.Errorf("chunk %d index = %d, want %d", i, got[i].Index, i)
		}
	}
}

func TestChunk_NoOverlap(t *testing.T) {
	t.Parallel()
	words := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	text := strings.Join(words, " ")
	got := Chunk(text, 5, 0)
	if len(got) != 2 {
		t.Fatalf("got %d chunks, want 2 (no overlap)", len(got))
	}
	if got[0].Content != "a b c d e" {
		t.Errorf("chunk 0 = %q", got[0].Content)
	}
	if got[1].Content != "f g h i j" {
		t.Errorf("chunk 1 = %q", got[1].Content)
	}
}

func TestChunk_OverlapClampedToHalfWindow(t *testing.T) {
	t.Parallel()
	// Pathological: overlap >= maxTokens. Function falls back to a
	// quarter-window overlap so we still make progress and don't loop.
	words := make([]string, 20)
	for i := range words {
		words[i] = "word"
	}
	got := Chunk(strings.Join(words, " "), 4, 10)
	if len(got) == 0 {
		t.Fatalf("got 0 chunks; expected progress with clamped overlap")
	}
	for i, c := range got {
		if c.TokenCount > 4 {
			t.Errorf("chunk %d token count = %d, exceeds max 4", i, c.TokenCount)
		}
	}
}

func TestChunk_DefaultsApplied(t *testing.T) {
	t.Parallel()
	// Long text exercising the defaults path: ~600 words with maxTokens=0
	// should yield 1+ chunk(s) of ≤512 tokens each.
	words := make([]string, 600)
	for i := range words {
		words[i] = "w"
	}
	got := Chunk(strings.Join(words, " "), 0, 0)
	if len(got) < 2 {
		t.Fatalf("expected ≥2 chunks for 600-word input, got %d", len(got))
	}
	for i, c := range got {
		if c.TokenCount > DefaultChunkMaxTokens {
			t.Errorf("chunk %d token count %d > default max %d", i, c.TokenCount, DefaultChunkMaxTokens)
		}
	}
}
