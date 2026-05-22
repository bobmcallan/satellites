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
