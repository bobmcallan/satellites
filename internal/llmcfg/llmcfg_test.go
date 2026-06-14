package llmcfg

import (
	"context"
	"testing"

	"github.com/bobmcallan/satellites/internal/variable"
)

// fakeGetter is a stub variable store keyed by name, scope=system only.
type fakeGetter map[string]variable.Variable

func (f fakeGetter) Get(_ context.Context, key variable.Key) (variable.Variable, error) {
	if v, ok := f[key.Name]; ok {
		return v, nil
	}
	return variable.Variable{}, variable.ErrNotFound
}

func TestResolver_Model(t *testing.T) {
	ctx := context.Background()
	store := fakeGetter{
		"agent.model": {Name: "agent.model", Value: "gemini-3.0-pro"},
		"synth.model": {Name: "synth.model", Value: "  "}, // blank → default
	}
	r := New(store, func(string) string { return "" })

	if got := r.Model(ctx, "agent", "DEFAULT"); got != "gemini-3.0-pro" {
		t.Errorf("agent model: got %q want gemini-3.0-pro (variable wins)", got)
	}
	if got := r.Model(ctx, "synth", "DEFAULT"); got != "DEFAULT" {
		t.Errorf("synth model: got %q want DEFAULT (blank value → default)", got)
	}
	if got := r.Model(ctx, "embedding", "DEFAULT"); got != "DEFAULT" {
		t.Errorf("embedding model: got %q want DEFAULT (unset → default)", got)
	}
}

func TestResolver_Secret(t *testing.T) {
	ctx := context.Background()
	store := fakeGetter{
		"gemini.api_key": {Name: "gemini.api_key", Value: "sk-from-variable", Secret: true},
	}
	env := map[string]string{
		"GEMINI_API_KEY":    "sk-from-env",
		"ANTHROPIC_API_KEY": "sk-anthropic-env",
	}
	r := New(store, func(k string) string { return env[k] })

	// Variable wins over env.
	if got := r.Secret(ctx, "gemini.api_key", "GEMINI_API_KEY"); got != "sk-from-variable" {
		t.Errorf("gemini key: got %q want sk-from-variable (variable wins)", got)
	}
	// Unset variable → env fallback.
	if got := r.Secret(ctx, "anthropic.api_key", "ANTHROPIC_API_KEY"); got != "sk-anthropic-env" {
		t.Errorf("anthropic key: got %q want sk-anthropic-env (env fallback)", got)
	}
	// Neither set → empty.
	if got := r.Secret(ctx, "missing.key", "MISSING_ENV"); got != "" {
		t.Errorf("missing key: got %q want empty", got)
	}
}

// TestResolver_NilStore confirms a resolver with no store degrades to the
// env fallback / default rather than panicking.
func TestResolver_NilStore(t *testing.T) {
	ctx := context.Background()
	r := New(nil, func(k string) string {
		if k == "GEMINI_API_KEY" {
			return "sk-env"
		}
		return ""
	})
	if got := r.Model(ctx, "agent", "DEFAULT"); got != "DEFAULT" {
		t.Errorf("nil store model: got %q want DEFAULT", got)
	}
	if got := r.Secret(ctx, "gemini.api_key", "GEMINI_API_KEY"); got != "sk-env" {
		t.Errorf("nil store secret: got %q want sk-env", got)
	}
}
