// Package embeddings is satellites' shared embeddings client + chunker. It
// owns the Embedder contract that document and ledger SearchSemantic paths
// depend on, the Gemini-backed production implementation, a deterministic
// stub for tests, and a token-windowing chunker.
//
// The Gemini path is a direct port of the satellites-v3 shape used in
// production for ~6 months; see internal/services/ingestion/embedder.go in
// the v3 tree. Pure stdlib HTTP — no SDK dependency.
package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"
)

// Embedder is the narrow contract callers depend on. Returns one
// `[]float32` per input text in the same order. Dimension reports the
// vector length so chunk-store schemas can be sized correctly.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Dimension() int
	Model() string
}

// GeminiBaseURL is the canonical Gemini endpoint root. Override via
// Config.BaseURL for tests against an httptest server.
const GeminiBaseURL = "https://generativelanguage.googleapis.com"

// DefaultGeminiModel is text-embedding-004 — 768-dim, used by satellites-v3
// in production. v4 keeps the same default unless the operator overrides it.
const DefaultGeminiModel = "text-embedding-004"

// DefaultGeminiDimension matches text-embedding-004's native output size.
const DefaultGeminiDimension = 768

// geminiMaxBatchSize is Gemini's documented per-request cap on
// batchEmbedContents items.
const geminiMaxBatchSize = 100

const (
	embedMaxRetries    = 3
	embedBaseBackoff   = 2 * time.Second
	embedBackoffFactor = 2
)

// GeminiEmbedder calls the Gemini batchEmbedContents endpoint. Safe for
// concurrent use — every call constructs its own request.
type GeminiEmbedder struct {
	apiKey    string
	model     string
	dimension int
	baseURL   string
	client    *http.Client
}

// NewGeminiEmbedder constructs an embedder against the supplied config.
// Empty Model / Dimension / BaseURL fall back to the package defaults so
// callers can pass a near-empty Config and get production behaviour.
func NewGeminiEmbedder(cfg Config) *GeminiEmbedder {
	if cfg.Model == "" {
		cfg.Model = DefaultGeminiModel
	}
	if cfg.Dimension <= 0 {
		cfg.Dimension = DefaultGeminiDimension
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = GeminiBaseURL
	}
	return &GeminiEmbedder{
		apiKey:    cfg.APIKey,
		model:     cfg.Model,
		dimension: cfg.Dimension,
		baseURL:   cfg.BaseURL,
		client:    &http.Client{Timeout: 60 * time.Second},
	}
}

// Dimension implements Embedder.
func (e *GeminiEmbedder) Dimension() int { return e.dimension }

// Model implements Embedder. Stamped on every chunk so a model change can
// be detected and trigger re-embedding under a follow-up backfill story.
func (e *GeminiEmbedder) Model() string { return e.model }

// geminiEmbedRequest is the batchEmbedContents request body.
type geminiEmbedRequest struct {
	Requests []geminiEmbedItem `json:"requests"`
}

type geminiEmbedItem struct {
	Model                string        `json:"model"`
	Content              geminiContent `json:"content"`
	OutputDimensionality int           `json:"outputDimensionality"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

// geminiEmbedResponse is the batchEmbedContents response shape.
type geminiEmbedResponse struct {
	Embeddings []geminiEmbedding `json:"embeddings"`
}

type geminiEmbedding struct {
	Values []float32 `json:"values"`
}

// Embed sends inputs to Gemini, batching at 100 items per request. Returns
// vectors in the same order as texts. Errors on any non-200 from Gemini
// after the retry budget is exhausted.
func (e *GeminiEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	out := make([][]float32, 0, len(texts))
	for i := 0; i < len(texts); i += geminiMaxBatchSize {
		end := i + geminiMaxBatchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch := texts[i:end]
		vecs, err := e.embedBatch(ctx, batch)
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

func (e *GeminiEmbedder) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	items := make([]geminiEmbedItem, len(texts))
	for i, t := range texts {
		items[i] = geminiEmbedItem{
			Model: "models/" + e.model,
			Content: geminiContent{
				Parts: []geminiPart{{Text: t}},
			},
			OutputDimensionality: e.dimension,
		}
	}
	body, err := json.Marshal(geminiEmbedRequest{Requests: items})
	if err != nil {
		return nil, fmt.Errorf("marshal gemini request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/v1beta/models/%s:batchEmbedContents", e.baseURL, e.model)

	var respBytes []byte
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create gemini request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		// API key flows in the query string per Gemini's HTTP convention.
		// We never log the request URL — see error-formatting in this
		// function which references the model name only.
		q := req.URL.Query()
		q.Set("key", e.apiKey)
		req.URL.RawQuery = q.Encode()

		resp, err := e.client.Do(req)
		if err != nil {
			// Mask any URL fragments the http library may surface.
			return nil, fmt.Errorf("gemini api call failed (model=%s)", e.model)
		}
		respBytes, err = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read gemini response: %w", err)
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < embedMaxRetries {
			backoff := retryBackoff(resp, attempt)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
				continue
			}
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("gemini api error %d (model=%s)", resp.StatusCode, e.model)
		}
		break
	}

	var decoded geminiEmbedResponse
	if err := json.Unmarshal(respBytes, &decoded); err != nil {
		return nil, fmt.Errorf("unmarshal gemini response: %w", err)
	}
	if len(decoded.Embeddings) != len(texts) {
		return nil, fmt.Errorf("gemini returned %d embeddings for %d texts", len(decoded.Embeddings), len(texts))
	}
	out := make([][]float32, len(decoded.Embeddings))
	for i, em := range decoded.Embeddings {
		out[i] = em.Values
	}
	return out, nil
}

// retryBackoff prefers Retry-After (seconds) when present; otherwise
// exponential 2s/4s/8s.
func retryBackoff(resp *http.Response, attempt int) time.Duration {
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	d := embedBaseBackoff
	for i := 0; i < attempt; i++ {
		d *= embedBackoffFactor
	}
	return d
}

// StubEmbedder is the deterministic test embedder. Hashes each input text
// into a vector via FNV; identical text → identical vector across runs.
// Different texts produce different vectors so cosine ordering is
// well-defined for unit tests.
type StubEmbedder struct {
	dim int
}

// NewStubEmbedder returns a stub of the given dimension. dim<=0 falls
// back to the production default so tests don't have to specify.
func NewStubEmbedder(dim int) *StubEmbedder {
	if dim <= 0 {
		dim = DefaultGeminiDimension
	}
	return &StubEmbedder{dim: dim}
}

// Dimension implements Embedder.
func (s *StubEmbedder) Dimension() int { return s.dim }

// Model implements Embedder.
func (s *StubEmbedder) Model() string { return "stub" }

// Embed returns one vector per text. The vector is deterministic in the
// text bytes — identical inputs map to identical outputs across runs and
// processes — so tests can assert ordering predictably.
func (s *StubEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = stubVector(t, s.dim)
	}
	return out, nil
}

// stubVector hashes text into dim float32 values in [-1, 1]. Each output
// dimension is seeded with i so identical text but different positions
// produce different values.
func stubVector(text string, dim int) []float32 {
	v := make([]float32, dim)
	for i := 0; i < dim; i++ {
		h := fnv.New32a()
		_, _ = h.Write([]byte(text))
		var idx [4]byte
		idx[0] = byte(i)
		idx[1] = byte(i >> 8)
		idx[2] = byte(i >> 16)
		idx[3] = byte(i >> 24)
		_, _ = h.Write(idx[:])
		// Map to [-1, 1].
		raw := h.Sum32()
		v[i] = (float32(raw)/float32(math.MaxUint32))*2 - 1
	}
	return v
}

// ErrDimensionMismatch is returned by Cosine when the two vectors have
// different lengths.
var ErrDimensionMismatch = errors.New("embeddings: vector dimension mismatch")

// Cosine returns the cosine similarity of two vectors in [-1, 1]. Returns
// 0 when either vector is zero-length to avoid NaN. Errors on dimension
// mismatch — callers should treat this as a bug, not a runtime condition.
func Cosine(a, b []float32) (float32, error) {
	if len(a) != len(b) {
		return 0, ErrDimensionMismatch
	}
	if len(a) == 0 {
		return 0, nil
	}
	var dot, na, nb float32
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0, nil
	}
	return dot / float32(math.Sqrt(float64(na))*math.Sqrt(float64(nb))), nil
}
