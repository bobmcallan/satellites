package embeddings

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/bobmcallan/satellites/internal/config"
)

// Provider names accepted in SATELLITES_EMBEDDINGS_PROVIDER. The `none`
// value is the idiomatic way to disable embeddings entirely (production
// deploy without a key, dev without internet) — store layer falls back to
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

// FromConfig projects the embeddings-relevant fields of *config.Config
// into the package-local Config. Story_b218cb81 made these knobs
// first-class on config.Config so production resolution flows through
// the shared env→TOML→default chain. Callers that previously used
// LoadFromEnv switch to this function with the resolved *config.Config.
func FromConfig(cfg *config.Config) Config {
	out := Config{
		Provider:  strings.ToLower(strings.TrimSpace(cfg.EmbeddingsProvider)),
		Model:     cfg.EmbeddingsModel,
		APIKey:    cfg.EmbeddingsAPIKey,
		BaseURL:   cfg.EmbeddingsBaseURL,
		Dimension: cfg.EmbeddingsDimension,
	}
	if out.Provider == "" {
		out.Provider = ProviderNone
	}
	return out
}

// LoadFromEnv reads the SATELLITES_EMBEDDINGS_* env vars into a Config.
// Empty env → Config{Provider:"none"}, which produces a nil Embedder
// from New().
//
// Deprecated: prefer FromConfig with a resolved *config.Config so the
// embeddings boot path participates in the shared env→TOML→default
// chain (story_b218cb81). LoadFromEnv is retained for tests that
// build Config directly without a full config.Config.
func LoadFromEnv() (Config, error) {
	cfg := Config{
		Provider: strings.ToLower(strings.TrimSpace(os.Getenv("SATELLITES_EMBEDDINGS_PROVIDER"))),
		Model:    os.Getenv("SATELLITES_EMBEDDINGS_MODEL"),
		APIKey:   os.Getenv("SATELLITES_EMBEDDINGS_API_KEY"),
		BaseURL:  os.Getenv("SATELLITES_EMBEDDINGS_BASE_URL"),
	}
	if cfg.Provider == "" {
		cfg.Provider = ProviderNone
	}
	if v := os.Getenv("SATELLITES_EMBEDDINGS_DIMENSION"); v != "" {
		d, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("invalid SATELLITES_EMBEDDINGS_DIMENSION %q: %w", v, err)
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
			return nil, fmt.Errorf("embeddings: SATELLITES_EMBEDDINGS_API_KEY required for provider=gemini")
		}
		return NewGeminiEmbedder(cfg), nil
	default:
		return nil, fmt.Errorf("embeddings: unknown provider %q", cfg.Provider)
	}
}
