package embeddings

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Provider names accepted in EMBEDDINGS_PROVIDER. The `none` value is the
// idiomatic way to disable embeddings entirely (production deploy without
// a key, dev without internet) — store layer falls back to
// ErrSemanticUnavailable.
const (
	ProviderGemini = "gemini"
	ProviderStub   = "stub"
	ProviderNone   = "none"
)

// Config carries the operator-supplied embeddings settings. Populated by
// LoadFromEnv at boot.
type Config struct {
	Provider  string
	Model     string
	APIKey    string
	Dimension int
	BaseURL   string
}

// LoadFromEnv reads the EMBEDDINGS_* env vars into a Config. Empty env
// → Config{Provider:"none"}, which produces a nil Embedder from New().
func LoadFromEnv() (Config, error) {
	cfg := Config{
		Provider: strings.ToLower(strings.TrimSpace(os.Getenv("EMBEDDINGS_PROVIDER"))),
		Model:    os.Getenv("EMBEDDINGS_MODEL"),
		APIKey:   os.Getenv("EMBEDDINGS_API_KEY"),
		BaseURL:  os.Getenv("EMBEDDINGS_BASE_URL"),
	}
	if cfg.Provider == "" {
		cfg.Provider = ProviderNone
	}
	if v := os.Getenv("EMBEDDINGS_DIMENSION"); v != "" {
		d, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("invalid EMBEDDINGS_DIMENSION %q: %w", v, err)
		}
		cfg.Dimension = d
	}
	return cfg, nil
}

// New constructs an Embedder for the given config. Returns nil when the
// provider is "none" or the config is empty — callers treat nil as "no
// semantic search" and route to ErrSemanticUnavailable.
func New(cfg Config) (Embedder, error) {
	switch cfg.Provider {
	case "", ProviderNone:
		return nil, nil
	case ProviderStub:
		return NewStubEmbedder(cfg.Dimension), nil
	case ProviderGemini:
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("embeddings: EMBEDDINGS_API_KEY required for provider=gemini")
		}
		return NewGeminiEmbedder(cfg), nil
	default:
		return nil, fmt.Errorf("embeddings: unknown provider %q", cfg.Provider)
	}
}
