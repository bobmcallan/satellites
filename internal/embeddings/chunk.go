package embeddings

import "strings"

// DefaultChunkMaxTokens is the default per-chunk word/token cap. 512 is
// the satellites-v3 default and a reasonable fit for text-embedding-004's
// 2048-token input ceiling — leaves headroom for the JSON envelope.
const DefaultChunkMaxTokens = 512

// DefaultChunkOverlap is the default overlap between adjacent chunks. 64
// matches v3 and provides enough context bleed across boundaries that a
// query whose subject straddles two chunks still scores both.
const DefaultChunkOverlap = 64

// TextChunk is a single chunk of text plus its position metadata.
type TextChunk struct {
	Content    string
	TokenCount int
	Index      int
}

// Chunk splits text into overlapping word-windows. Returns nil for empty
// or whitespace-only input. Identical to the v3 word-window chunker —
// `strings.Fields` for tokenisation, `start += maxTokens - overlap` for
// the stride. Word ≈ token at ~0.75 ratio is a deliberate simplification;
// good enough for the substring-replacement we're shipping.
//
// Defaults apply when maxTokens or overlap are non-positive: callers can
// pass 0/0 to get the shipped defaults.
func Chunk(text string, maxTokens, overlap int) []TextChunk {
	if maxTokens <= 0 {
		maxTokens = DefaultChunkMaxTokens
	}
	if overlap < 0 {
		overlap = DefaultChunkOverlap
	}
	if overlap >= maxTokens {
		// Pathological config — fall back to a quarter window so we
		// don't loop forever.
		overlap = maxTokens / 4
	}
	if text == "" {
		return nil
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}

	stride := maxTokens - overlap
	if stride <= 0 {
		stride = 1
	}

	var out []TextChunk
	for start, idx := 0, 0; start < len(words); idx++ {
		end := start + maxTokens
		if end > len(words) {
			end = len(words)
		}
		seg := words[start:end]
		out = append(out, TextChunk{
			Content:    strings.Join(seg, " "),
			TokenCount: len(seg),
			Index:      idx,
		})
		if end == len(words) {
			break
		}
		start += stride
	}
	return out
}
