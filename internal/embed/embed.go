// Package embed is the workspace-corpus embedding substrate (sty_7f4f7e11):
// chunk a document, embed the chunks with Gemini, store the vectors in
// Postgres, and rank by cosine similarity in Go (brute force — no pgvector).
// Ported from satellites-v3's ingestion service, adapted to this repo's
// Postgres store. The Gemini integration is server-side because the manager
// flow is Claude web + MCP, which has no client-side compute to embed with.
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Embedder turns texts into vectors. The interface keeps the service testable
// and lets an absent key degrade to a nil embedder (graceful no-op).
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Dimension() int
}

// GeminiEmbedder calls Gemini's batchEmbedContents API. Credentials resolve
// per Embed via provide, so the api key and model are read at request time
// (substrate key-values, env fallback). The vector dimension stays fixed at
// construction — it is coupled to the stored vectors and must not change
// under a populated corpus.
type GeminiEmbedder struct {
	provide   func(context.Context) (apiKey, model string)
	dimension int
	baseURL   string
	client    *http.Client
}

// DefaultModel / DefaultDimension are Gemini's embedding defaults.
// gemini-embedding-001 exposes embedContent + outputDimensionality (Matryoshka
// truncation); 768 is a supported dimension.
const (
	DefaultModel     = "gemini-embedding-001"
	DefaultDimension = 768
	defaultBaseURL   = "https://generativelanguage.googleapis.com"
)

// NewGeminiEmbedder builds a Gemini embedding client from fixed credentials.
// An empty model/dimension falls back to the defaults. Kept for callers with
// static config.
func NewGeminiEmbedder(apiKey, model string, dimension int) *GeminiEmbedder {
	if strings.TrimSpace(model) == "" {
		model = DefaultModel
	}
	return NewGeminiEmbedderFunc(func(context.Context) (string, string) { return apiKey, model }, dimension)
}

// NewGeminiEmbedderFunc builds an embedder from a per-request credential
// provider (api key + model resolved at Embed time). The dimension is fixed.
func NewGeminiEmbedderFunc(provide func(context.Context) (apiKey, model string), dimension int) *GeminiEmbedder {
	if dimension <= 0 {
		dimension = DefaultDimension
	}
	return &GeminiEmbedder{
		provide:   provide,
		dimension: dimension,
		baseURL:   defaultBaseURL,
		client:    &http.Client{Timeout: 60 * time.Second},
	}
}

// Dimension returns the embedding vector size.
func (e *GeminiEmbedder) Dimension() int { return e.dimension }

type geminiEmbedRequest struct {
	Model                string        `json:"model"`
	Content              geminiContent `json:"content"`
	OutputDimensionality int           `json:"outputDimensionality,omitempty"`
}
type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}
type geminiPart struct {
	Text string `json:"text"`
}
type geminiEmbedResponse struct {
	Embedding geminiEmbedding `json:"embedding"`
}
type geminiEmbedding struct {
	Values []float32 `json:"values"`
}

// Embed embeds each text via Gemini's embedContent endpoint (one call per text;
// the current embedding models expose embedContent + asyncBatchEmbedContent, not
// the older synchronous batchEmbedContents). A corpus chunk count is small, so
// per-text calls are fine.
func (e *GeminiEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	apiKey, model := e.provide(ctx)
	if strings.TrimSpace(model) == "" {
		model = DefaultModel
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("embed: gemini api key not configured")
	}
	out := make([][]float32, 0, len(texts))
	for _, t := range texts {
		v, err := e.embedOne(ctx, t, apiKey, model)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

const (
	embedMaxRetries    = 3
	embedBaseBackoff   = 2 * time.Second
	embedBackoffFactor = 2
)

func (e *GeminiEmbedder) embedOne(ctx context.Context, text, apiKey, model string) ([]float32, error) {
	bodyBytes, err := json.Marshal(geminiEmbedRequest{
		Model:                "models/" + model,
		Content:              geminiContent{Parts: []geminiPart{{Text: text}}},
		OutputDimensionality: e.dimension,
	})
	if err != nil {
		return nil, fmt.Errorf("embed: marshal gemini request: %w", err)
	}
	endpoint := fmt.Sprintf("%s/v1beta/models/%s:embedContent", e.baseURL, model)

	var respBytes []byte
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, fmt.Errorf("embed: create gemini request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		q := req.URL.Query()
		q.Set("key", apiKey)
		req.URL.RawQuery = q.Encode()

		resp, err := e.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("embed: gemini call: %w", err)
		}
		respBytes, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("embed: read gemini response: %w", err)
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < embedMaxRetries {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(retryBackoff(resp, attempt)):
				continue
			}
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("embed: gemini api error %d: %s", resp.StatusCode, strings.TrimSpace(string(respBytes)))
		}
		break
	}

	var gr geminiEmbedResponse
	if err := json.Unmarshal(respBytes, &gr); err != nil {
		return nil, fmt.Errorf("embed: unmarshal gemini response: %w", err)
	}
	if len(gr.Embedding.Values) == 0 {
		return nil, fmt.Errorf("embed: gemini returned an empty embedding")
	}
	return gr.Embedding.Values, nil
}

func retryBackoff(resp *http.Response, attempt int) time.Duration {
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	backoff := embedBaseBackoff
	for i := 0; i < attempt; i++ {
		backoff *= embedBackoffFactor
	}
	return backoff
}

// Chunker splits text into overlapping word-window chunks (token ≈ word).
// Ported from satellites-v3; MaxTokens/Overlap are configuration (AC3).
type Chunker struct {
	MaxTokens int
	Overlap   int
}

// TextChunk is a piece of text ready for embedding.
type TextChunk struct {
	Index      int
	Content    string
	TokenCount int
}

// Chunk splits text into overlapping segments. An empty/zero config falls back
// to 512/64. Overlap >= MaxTokens is clamped to a quarter window.
func (c Chunker) Chunk(text string) []TextChunk {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	maxWords := c.MaxTokens
	if maxWords <= 0 {
		maxWords = 512
	}
	overlapWords := c.Overlap
	if overlapWords < 0 {
		overlapWords = 0
	}
	if overlapWords >= maxWords {
		overlapWords = maxWords / 4
	}
	var chunks []TextChunk
	idx, start := 0, 0
	for start < len(words) {
		end := start + maxWords
		if end > len(words) {
			end = len(words)
		}
		segment := words[start:end]
		chunks = append(chunks, TextChunk{Index: idx, Content: strings.Join(segment, " "), TokenCount: len(segment)})
		idx++
		if end == len(words) {
			break
		}
		advance := maxWords - overlapWords
		if advance <= 0 {
			advance = 1
		}
		start += advance
	}
	return chunks
}

// cosineSimilarity returns the cosine of the angle between a and b, or 0 when
// either is zero-length or a zero vector. The brute-force ranking primitive.
func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// topK ranks chunks by cosine against query, returning the best k (descending).
func topK(query []float64, chunks []ScoredChunk, k int) []ScoredChunk {
	if k <= 0 {
		k = 5
	}
	for i := range chunks {
		chunks[i].Score = cosineSimilarity(query, chunks[i].Embedding)
	}
	sort.SliceStable(chunks, func(i, j int) bool { return chunks[i].Score > chunks[j].Score })
	if len(chunks) > k {
		chunks = chunks[:k]
	}
	return chunks
}

// float32sToFloat64s converts an embedding for storage/cosine in float64.
func float32sToFloat64s(in []float32) []float64 {
	out := make([]float64, len(in))
	for i, v := range in {
		out[i] = float64(v)
	}
	return out
}
