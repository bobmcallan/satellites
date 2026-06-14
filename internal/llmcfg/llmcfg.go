// Package llmcfg resolves the LLM model selection and API keys at request
// time from the variable substrate, with an environment-variable fallback.
//
// Model names are plain stored-system variables ("<kind>.model"); API keys
// are SECRET variables read directly from the store (bypassing the
// read-verb redaction). Both fall back to an environment variable so a
// deployed env / fly secret keeps working until the substrate value is set,
// and the substrate value wins when present. Resolution happens per call so
// rotating a key or switching the model takes effect without a redeploy.
package llmcfg

import (
	"context"
	"os"
	"strings"

	"github.com/bobmcallan/satellites/internal/variable"
)

// variableGetter is the slice of *variable.Store the resolver needs —
// narrowed to an interface so the precedence logic is unit-testable without
// a database. *variable.Store satisfies it.
type variableGetter interface {
	Get(ctx context.Context, key variable.Key) (variable.Variable, error)
}

// Resolver reads runtime LLM configuration from the variable store.
type Resolver struct {
	store variableGetter
	env   func(string) string
}

// New builds a Resolver over the variable store. env defaults to os.Getenv
// when nil (injectable for tests).
func New(store variableGetter, env func(string) string) *Resolver {
	if env == nil {
		env = os.Getenv
	}
	return &Resolver{store: store, env: env}
}

// Model returns the configured model for kind ("agent"|"synth"|"embedding"):
// the stored-system variable "<kind>.model" when set and non-empty, else def.
func (r *Resolver) Model(ctx context.Context, kind, def string) string {
	if v, ok := r.systemValue(ctx, kind+".model"); ok {
		return v
	}
	return def
}

// Secret returns the secret variable `name` read directly from the store,
// else the environment fallback env[envKey]. The substrate value wins.
func (r *Resolver) Secret(ctx context.Context, name, envKey string) string {
	if v, ok := r.systemValue(ctx, name); ok {
		return v
	}
	if r != nil && envKey != "" {
		return strings.TrimSpace(r.env(envKey))
	}
	return ""
}

// systemValue reads a stored-system variable, returning ok=false when the
// resolver/store is absent, the row is missing, or the value is blank.
func (r *Resolver) systemValue(ctx context.Context, name string) (string, bool) {
	if r == nil || r.store == nil {
		return "", false
	}
	v, err := r.store.Get(ctx, variable.Key{Scope: variable.ScopeSystem, Name: name})
	if err != nil {
		return "", false
	}
	if s := strings.TrimSpace(v.Value); s != "" {
		return s, true
	}
	return "", false
}
