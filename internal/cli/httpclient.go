package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/cliconfig"
)

// httpDispatch sends a verb call to a remote satellites-server over
// POST /api/v1/exec/<name>. The api-key in cfg.Auth.Token authenticates
// against the server's standard bearer middleware.
//
// Used by cmd_exec.go when cliconfig.IsConfigured(). When the config
// file is absent or incomplete, cmd_exec.go falls back to
// verb.Dispatch (the in-process registry).
func httpDispatch(cfg cliconfig.Config, name string, req json.RawMessage) (json.RawMessage, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.ServerURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("cli: server_url missing in config")
	}
	if cfg.Auth.Token == "" {
		return nil, fmt.Errorf("cli: auth.token missing in config")
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
	httpReq.Header.Set("Authorization", "Bearer "+cfg.Auth.Token)
	httpReq.Header.Set("Content-Type", "application/json")

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
		return nil, fmt.Errorf("cli: 401 unauthorized — check auth.token in %s", configHintPath())
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

// configHintPath is best-effort prose used in error messages — tells
// the operator where the config that owns the bearer probably lives.
// Doesn't try to be authoritative; we just want a starting point for
// the human reading the error.
func configHintPath() string { return ".satellites/satellites.toml" }
