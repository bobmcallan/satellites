package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/bobmcallan/satellites/internal/cliconfig"
	"github.com/bobmcallan/satellites/internal/verb"
)

// selfHealSkipKey marks a context as already inside the self-heal
// path. dispatchVerb checks this before attempting to heal again,
// preventing infinite recursion through the project_match call.
type selfHealSkipKey struct{}

// withSelfHealSkip returns ctx tagged so dispatchVerb skips the heal
// step. Used internally by selfHealProjectID when calling project_match.
func withSelfHealSkip(ctx context.Context) context.Context {
	return context.WithValue(ctx, selfHealSkipKey{}, true)
}

// selfHealAlreadyAttempted reports whether ctx was tagged via
// withSelfHealSkip.
func selfHealAlreadyAttempted(ctx context.Context) bool {
	v, _ := ctx.Value(selfHealSkipKey{}).(bool)
	return v
}

// verbsThatNeverNeedProjectID are dispatch targets where the
// pre-call heal would just be wasted work — they don't take a
// project_id. Keeps the heal silent for `satellites version`,
// `satellites project match`, etc.
var verbsThatNeverNeedProjectID = map[string]bool{
	"version":          true,
	"project_match":    true,
	"project_create":   true,
	"project_list":     true,
	"project_get":      true,
	"workspace_list":   true,
	"workspace_get":    true,
	"workspace_create": true,
}

// dispatchVerb is the shared verb-call path used by `satellites exec`
// and the typed subcommands (story, project, …). It applies the same
// config-aware routing: when cliconfig.IsConfigured() the call goes
// over HTTP to the configured satellites-server; otherwise it runs
// against the in-process verb registry.
//
// Before dispatching it runs the project_id self-heal if the loaded
// config has no project_id and the verb is one that may need one.
// Self-heal failure is silent — the dispatched verb still gets its
// chance to fail with its own error message (the contract from
// sty_1d4bf7eb AC #3).
//
// The returned bytes are the verb's JSON response, unmodified.
func dispatchVerb(ctx context.Context, name string, req json.RawMessage, configPath, userID string) (json.RawMessage, error) {
	cfg, resolvedPath, err := cliconfig.Load(configPath)
	if err != nil && !errors.Is(err, cliconfig.ErrNotFound) {
		return nil, err
	}

	if shouldAttemptSelfHeal(ctx, name, cfg, resolvedPath) {
		if _, healErr := selfHealProjectID(withSelfHealSkip(ctx), cfg, resolvedPath, userID); healErr == nil {
			// Reload so downstream sees the healed value (HTTP routing
			// reads cfg; the in-process registry doesn't care).
			cfg, _, _ = cliconfig.Load(configPath)
		}
	}

	switch {
	case cfg.IsConfigured():
		return httpDispatch(cfg, name, req)
	}
	return verb.Dispatch(stampCallerUser(ctx, resolveCallerUserID(userID)), name, req)
}

func shouldAttemptSelfHeal(ctx context.Context, verbName string, cfg cliconfig.Config, resolvedPath string) bool {
	if selfHealAlreadyAttempted(ctx) {
		return false
	}
	if resolvedPath == "" {
		return false
	}
	if strings.TrimSpace(cfg.ProjectID) != "" {
		return false
	}
	if verbsThatNeverNeedProjectID[verbName] {
		return false
	}
	return true
}
