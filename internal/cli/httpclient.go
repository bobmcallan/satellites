package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/cliconfig"
)

// correlationEnvHeaders lists the env-var → HTTP-header pairs the CLI
// transport stamps onto every outbound request when the env var is
// set. A harness that exports these env vars (on itself and a spawned
// claude subprocess) has every verb call —
// from inside this CLI process or from the dispatched agent — carry
// the same run / session / story / project / workspace correlation
// the satellites-server middleware (sty_0006f5f5) lifts onto request
// context. The arbor LedgerHandler then tags every log row written
// inside that request with these ids, so the portal `/ledger` page
// can isolate one run end-to-end.
//
// Header names mirror the constants in internal/correlation; kept
// here as literal strings to honour the CLI layering rule (no
// internal/correlation import from cli/).
var correlationEnvHeaders = [...]struct {
	Env, Header string
}{
	{"SATELLITES_RUN_ID", "X-Satellites-Run-ID"},
	{"SATELLITES_SESSION_ID", "X-Satellites-Session-ID"},
	{"SATELLITES_STORY_ID", "X-Satellites-Story-ID"},
	{"SATELLITES_PROJECT_ID", "X-Satellites-Project-ID"},
	{"SATELLITES_WORKSPACE_ID", "X-Satellites-Workspace-ID"},
}

// httpDispatch sends a verb call to a remote satellites-server over
// POST /api/v1/exec/<name>. The api-key in cfg.Token — resolved from the
// user-level credential store (provisioned by `satellites auth`) —
// authenticates against the server's standard bearer middleware.
//
// Used by cmd_exec.go when cliconfig.IsConfigured(). When the config
// file is absent or incomplete, cmd_exec.go falls back to
// verb.Dispatch (the in-process registry).
func httpDispatch(cfg cliconfig.Config, name string, req json.RawMessage) (json.RawMessage, error) {
	return httpDispatchWithToken(cfg, name, req, cfg.Token)
}

// httpDispatchWithToken is httpDispatch with an explicit bearer token,
// overriding cfg.Token. The reviewer gate uses this to send its
// spine writes (review_*, status_transition) and the status patch under
// a minted reviewer key while the operator's stored key stays executor
// (sty_e16f0553). The server URL + correlation headers come from cfg as
// usual; only the Authorization bearer differs.
func httpDispatchWithToken(cfg cliconfig.Config, name string, req json.RawMessage, token string) (json.RawMessage, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.ServerURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("cli: server_url missing in config")
	}
	if token == "" {
		return nil, fmt.Errorf("cli: no api-key — run 'satellites auth' to authenticate")
	}
	if strings.ContainsAny(name, "/?#") {
		return nil, fmt.Errorf("cli: invalid verb name %q", name)
	}

	body := req
	if body == nil {
		body = json.RawMessage(`{}`)
	}

	httpReq, err := http.NewRequest("POST", baseURL+"/api/v1/exec/"+name, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("cli: build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")
	for _, p := range correlationEnvHeaders {
		if v := strings.TrimSpace(os.Getenv(p.Env)); v != "" {
			httpReq.Header.Set(p.Header, v)
		}
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("cli: dispatch %s: %w", name, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("cli: read response: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("cli: 401 unauthorized — run 'satellites auth' to (re)authenticate")
	}
	if resp.StatusCode >= 400 {
		var errEnv struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(respBody, &errEnv) == nil && errEnv.Error != "" {
			return nil, fmt.Errorf("cli: %s: %s", name, errEnv.Error)
		}
		return nil, fmt.Errorf("cli: %s: HTTP %d: %s", name, resp.StatusCode, string(respBody))
	}
	return json.RawMessage(respBody), nil
}
