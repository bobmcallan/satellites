package embeddings

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStubEmbedder_Deterministic(t *testing.T) {
	t.Parallel()
	e := NewStubEmbedder(0) // default dim
	out1, err := e.Embed(context.Background(), []string{"alpha", "beta"})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	out2, err := e.Embed(context.Background(), []string{"alpha", "beta"})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(out1) != 2 || len(out2) != 2 {
		t.Fatalf("expected 2 vectors, got %d/%d", len(out1), len(out2))
	}
	for i := 0; i < e.Dimension(); i++ {
		if out1[0][i] != out2[0][i] {
			t.Fatalf("non-deterministic at dim %d: %v vs %v", i, out1[0][i], out2[0][i])
		}
	}
	// Different texts → different vectors.
	if out1[0][0] == out1[1][0] && out1[0][1] == out1[1][1] {
		t.Fatalf("alpha/beta produced identical vectors")
	}
}

func TestStubEmbedder_Dimension(t *testing.T) {
	t.Parallel()
	e := NewStubEmbedder(16)
	if e.Dimension() != 16 {
		t.Fatalf("expected dim=16, got %d", e.Dimension())
	}
	out, _ := e.Embed(context.Background(), []string{"x"})
	if len(out[0]) != 16 {
		t.Fatalf("vector len=%d, want 16", len(out[0]))
	}
}

func TestCosine_KnownVectors(t *testing.T) {
	t.Parallel()
	a := []float32{1, 0, 0}
	b := []float32{1, 0, 0}
	c := []float32{0, 1, 0}
	d := []float32{-1, 0, 0}
	cases := []struct {
		name string
		x, y []float32
		want float32
	}{
		{"identical", a, b, 1},
		{"orthogonal", a, c, 0},
		{"opposite", a, d, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Cosine(tc.x, tc.y)
			if err != nil {
				t.Fatalf("cosine: %v", err)
			}
			if got != tc.want {
				t.Fatalf("cosine(%v,%v)=%v want %v", tc.x, tc.y, got, tc.want)
			}
		})
	}
}

func TestCosine_DimensionMismatch(t *testing.T) {
	t.Parallel()
	if _, err := Cosine([]float32{1, 0}, []float32{1, 0, 0}); err == nil {
		t.Fatalf("expected ErrDimensionMismatch")
	}
}

func TestCosine_ZeroVector(t *testing.T) {
	t.Parallel()
	got, err := Cosine([]float32{0, 0, 0}, []float32{1, 1, 1})
	if err != nil {
		t.Fatalf("cosine: %v", err)
	}
	if got != 0 {
		t.Fatalf("expected 0 for zero vector, got %v", got)
	}
}

func TestGeminiEmbedder_HappyPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Assert the request shape so we know the wire format hasn't drifted.
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if !strings.Contains(r.URL.Path, ":batchEmbedContents") {
			t.Errorf("path = %s, want :batchEmbedContents suffix", r.URL.Path)
		}
		if r.URL.Query().Get("key") == "" {
			t.Errorf("missing api key query param")
		}
		var req geminiEmbedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode: %v", err)
		}
		resp := geminiEmbedResponse{Embeddings: make([]geminiEmbedding, len(req.Requests))}
		for i := range req.Requests {
			resp.Embeddings[i] = geminiEmbedding{Values: []float32{float32(i + 1), 0, 0}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	e := NewGeminiEmbedder(Config{
		Provider: ProviderGemini,
		Model:    "test-model",
		APIKey:   "secret",
		BaseURL:  srv.URL,
	})
	vecs, err := e.Embed(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(vecs) != 3 {
		t.Fatalf("got %d vectors, want 3", len(vecs))
	}
	for i, v := range vecs {
		if v[0] != float32(i+1) {
			t.Errorf("vec[%d][0] = %v, want %d", i, v[0], i+1)
		}
	}
}

func TestGeminiEmbedder_RetryOn429(t *testing.T) {
	t.Parallel()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(geminiEmbedResponse{
			Embeddings: []geminiEmbedding{{Values: []float32{1, 0, 0}}},
		})
	}))
	defer srv.Close()

	e := NewGeminiEmbedder(Config{
		Provider: ProviderGemini,
		Model:    "test-model",
		APIKey:   "secret",
		BaseURL:  srv.URL,
	})
	start := time.Now()
	vecs, err := e.Embed(context.Background(), []string{"only"})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(vecs) != 1 {
		t.Fatalf("got %d vectors, want 1", len(vecs))
	}
	if elapsed := time.Since(start); elapsed < time.Second {
		t.Errorf("retry happened too fast (%v); Retry-After:1 should pause ~1s", elapsed)
	}
	if atomic.LoadInt32(&hits) != 2 {
		t.Errorf("expected 2 server hits, got %d", hits)
	}
}

func TestGeminiEmbedder_ServerError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	e := NewGeminiEmbedder(Config{
		Provider: ProviderGemini,
		Model:    "test-model",
		APIKey:   "secret",
		BaseURL:  srv.URL,
	})
	_, err := e.Embed(context.Background(), []string{"x"})
	if err == nil {
		t.Fatalf("expected error on 400")
	}
	if strings.Contains(err.Error(), "secret") {
		t.Errorf("error leaked api key: %v", err)
	}
}

func TestGeminiEmbedder_BatchSplit(t *testing.T) {
	t.Parallel()
	var batches int32
	var totalItems int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&batches, 1)
		var req geminiEmbedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode: %v", err)
		}
		atomic.AddInt32(&totalItems, int32(len(req.Requests)))
		if len(req.Requests) > geminiMaxBatchSize {
			t.Errorf("batch size %d exceeds cap %d", len(req.Requests), geminiMaxBatchSize)
		}
		resp := geminiEmbedResponse{Embeddings: make([]geminiEmbedding, len(req.Requests))}
		for i := range req.Requests {
			resp.Embeddings[i] = geminiEmbedding{Values: []float32{float32(i)}}
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	e := NewGeminiEmbedder(Config{Provider: ProviderGemini, Model: "x", APIKey: "k", BaseURL: srv.URL})
	texts := make([]string, 250)
	for i := range texts {
		texts[i] = "t"
	}
	vecs, err := e.Embed(context.Background(), texts)
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(vecs) != 250 {
		t.Fatalf("got %d vectors, want 250", len(vecs))
	}
	if got := atomic.LoadInt32(&batches); got != 3 {
		t.Errorf("batches=%d, want 3 (100+100+50)", got)
	}
	if got := atomic.LoadInt32(&totalItems); got != 250 {
		t.Errorf("items across batches=%d, want 250", got)
	}
}

func TestNew_ProviderRouting(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		cfg     Config
		wantNil bool
		wantErr bool
	}{
		{"none", Config{Provider: ProviderNone}, true, false},
		{"empty", Config{}, true, false},
		{"stub", Config{Provider: ProviderStub, Dimension: 16}, false, false},
		{"gemini-no-key", Config{Provider: ProviderGemini}, true, true},
		{"gemini-with-key", Config{Provider: ProviderGemini, APIKey: "k"}, false, false},
		{"unknown", Config{Provider: "magic"}, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			emb, err := New(tc.cfg)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantNil && emb != nil {
				t.Errorf("expected nil embedder")
			}
			if !tc.wantNil && emb == nil {
				t.Errorf("expected non-nil embedder")
			}
		})
	}
}
