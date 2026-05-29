package cli

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/bobmcallan/satellites/internal/cliconfig"
	"github.com/bobmcallan/satellites/internal/verb"
)

// dispatchVerb is the shared verb-call path used by `satellites exec`
// and the typed subcommands (story, project, …). It applies the same
// config-aware routing: when cliconfig.IsConfigured() the call goes
// over HTTP to the configured satellites-server; otherwise it runs
// against the in-process verb registry.
//
// The returned bytes are the verb's JSON response, unmodified.
func dispatchVerb(ctx context.Context, name string, req json.RawMessage, configPath, userID string) (json.RawMessage, error) {
	cfg, _, err := cliconfig.Load(configPath)
	switch {
	case err == nil && cfg.IsConfigured():
		return httpDispatch(cfg, name, req)
	case err != nil && !errors.Is(err, cliconfig.ErrNotFound):
		return nil, err
	}
	return verb.Dispatch(stampCallerUser(ctx, resolveCallerUserID(userID)), name, req)
}

// dispatchVerbAs is dispatchVerb with an explicit bearer override. When
// apiKey is non-empty and the client is server-configured, the call goes
// out under apiKey instead of the TOML's token — the path the reviewer
// gate uses to write its spine rows + status patch under a minted
// reviewer key while the operator's stored key stays executor
// (sty_e16f0553). Empty apiKey, or an unconfigured (in-process) client,
// behaves exactly as dispatchVerb.
func dispatchVerbAs(ctx context.Context, name string, req json.RawMessage, configPath, userID, apiKey string) (json.RawMessage, error) {
	if apiKey == "" {
		return dispatchVerb(ctx, name, req, configPath, userID)
	}
	cfg, _, err := cliconfig.Load(configPath)
	switch {
	case err == nil && cfg.IsConfigured():
		return httpDispatchWithToken(cfg, name, req, apiKey)
	case err != nil && !errors.Is(err, cliconfig.ErrNotFound):
		return nil, err
	}
	// In-process registry has no bearer concept; the role gate treats an
	// empty api-key role as unrestricted, so the spine writes pass there.
	return verb.Dispatch(stampCallerUser(ctx, resolveCallerUserID(userID)), name, req)
}
