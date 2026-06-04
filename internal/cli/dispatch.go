package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/cliconfig"
	"github.com/bobmcallan/satellites/internal/measure"
	"github.com/bobmcallan/satellites/internal/verb"
)

// dispatchVerb is the shared verb-call path used by `satellites exec`
// and the typed subcommands (story, project, …). It applies the same
// config-aware routing: when cliconfig.IsConfigured() the call goes
// over HTTP to the configured satellites-server; otherwise it runs
// against the in-process verb registry. The TOML's token authenticates;
// gate enactment runs under the operator's own admin auth (the server
// authorizes status_transition / review_* by the admin user behind the
// call), so there is no separate reviewer-key bearer to layer on.
//
// When measure mode is enabled (default ON), every client verb call is
// recorded to the in-repo session log — the client-side half of measure
// mode's data collection (the agent-side hook is separate).
//
// The returned bytes are the verb's JSON response, unmodified.
func dispatchVerb(ctx context.Context, name string, req json.RawMessage, configPath, userID string) (json.RawMessage, error) {
	cfg, path, err := cliconfig.Load(configPath)
	configFound := err == nil
	switch {
	case err == nil && cfg.IsConfigured():
		resp, derr := httpDispatch(cfg, name, req)
		measureLogVerb(configFound, cfg, path, name, derr)
		return resp, derr
	case err != nil && !errors.Is(err, cliconfig.ErrNotFound):
		return nil, err
	}
	resp, derr := verb.Dispatch(stampCallerUser(ctx, resolveCallerUserID(userID)), name, req)
	measureLogVerb(configFound, cfg, path, name, derr)
	return resp, derr
}

// measureLogVerb records one client verb call to the in-repo measure log when
// measure mode is enabled. Best-effort: a logging failure never affects the
// verb's result. Only runs when a config file was actually found (configFound)
// so an unconfigured CWD never spawns stray .satellites/logs.
func measureLogVerb(configFound bool, cfg cliconfig.Config, configPath, name string, callErr error) {
	if !configFound || !cfg.Measure.IsEnabled() {
		return
	}
	repoRoot := cliconfig.RepoRootFromConfigPath(configPath)
	_ = measure.AppendEvent(cfg.MeasureLogDir(repoRoot), measureSessionID(), map[string]any{
		"type": "client_verb",
		"verb": name,
		"ts":   time.Now().UTC().Format(time.RFC3339Nano),
		"ok":   callErr == nil,
	})
}

// measureSessionID groups a session's log lines. The agent-side hook (and the
// gate dispatcher) set SATELLITES_SESSION_ID; absent it, client calls log under
// a stable "client" bucket.
func measureSessionID() string {
	if s := strings.TrimSpace(os.Getenv("SATELLITES_SESSION_ID")); s != "" {
		return s
	}
	return "client"
}
