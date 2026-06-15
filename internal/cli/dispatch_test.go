package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/cliconfig"
)

// dispatchTestServer stands up a stub satellites-server that records the
// Authorization bearer of each /api/v1/exec call, plus a non-secret TOML
// pointing at it. The operator's executor token ("tok-operator") lives in
// an isolated credential store (resolved by server_url at Load), matching
// the post-`satellites auth` model. Returns the config path and a pointer
// to the last seen bearer.
func dispatchTestServer(t *testing.T) (configPath string, lastBearer *string) {
	t.Helper()
	var seen string
	lastBearer = &seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := cliconfig.SaveCredential(cliconfig.Credential{ServerURL: srv.URL, Token: "tok-operator", Role: "executor"}); err != nil {
		t.Fatalf("save credential: %v", err)
	}

	dir := t.TempDir()
	configPath = filepath.Join(dir, "satellites.toml")
	toml := "server_url = \"" + srv.URL + "\"\n" +
		"project_id = \"proj_t\"\n"
	if err := os.WriteFile(configPath, []byte(toml), 0o600); err != nil {
		t.Fatalf("write toml: %v", err)
	}
	return configPath, lastBearer
}

// TestDispatchVerb_UsesOperatorToken confirms a configured client
// authenticates with the operator's stored token. Gate enactment now runs
// under the operator's own admin auth (status / review writes are authorized
// by the admin user, not a minted reviewer key), so there is no separate
// reviewer-key bearer to override it.
func TestDispatchVerb_UsesOperatorToken(t *testing.T) {
	configPath, lastBearer := dispatchTestServer(t)

	if _, err := dispatchVerb(context.Background(), "document_get",
		json.RawMessage(`{"id":"sty_x"}`), configPath, ""); err != nil {
		t.Fatalf("dispatchVerb: %v", err)
	}
	if *lastBearer != "tok-operator" {
		t.Fatalf("bearer = %q, want the operator TOML token", *lastBearer)
	}
}

// headlessTestServer is dispatchTestServer without a stored credential: a stub
// server recording the bearer + a TOML naming it, under an isolated XDG dir so
// no credential file exists. Exercises the SATELLITES_API_KEY headless path.
func headlessTestServer(t *testing.T) (configPath string, lastBearer *string) {
	t.Helper()
	var seen string
	lastBearer = &seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)

	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // isolated → no credential present
	dir := t.TempDir()
	configPath = filepath.Join(dir, "satellites.toml")
	toml := "server_url = \"" + srv.URL + "\"\nproject_id = \"proj_t\"\n"
	if err := os.WriteFile(configPath, []byte(toml), 0o600); err != nil {
		t.Fatalf("write toml: %v", err)
	}
	return configPath, lastBearer
}

// TestDispatchVerb_APIKeyEnvOverride confirms the headless path (sty_1121f1b2):
// with a server_url TOML, no credential file, and SATELLITES_API_KEY set, the
// call routes remote and authenticates with the env key.
func TestDispatchVerb_APIKeyEnvOverride(t *testing.T) {
	configPath, lastBearer := headlessTestServer(t)
	t.Setenv("SATELLITES_API_KEY", "env-executor-key")

	if _, err := dispatchVerb(context.Background(), "system_status",
		json.RawMessage(`{}`), configPath, ""); err != nil {
		t.Fatalf("dispatchVerb: %v", err)
	}
	if *lastBearer != "env-executor-key" {
		t.Fatalf("bearer = %q, want the SATELLITES_API_KEY value", *lastBearer)
	}
}

// TestDispatchVerb_NoTokenSurfacesError confirms a server_url config with no
// token (no credential, no env) routes remote and returns the no-api-key error
// naming both auth paths — not a silent in-process fallback (sty_1121f1b2 AC5).
func TestDispatchVerb_NoTokenSurfacesError(t *testing.T) {
	configPath, _ := headlessTestServer(t)
	t.Setenv("SATELLITES_API_KEY", "")

	_, err := dispatchVerb(context.Background(), "system_status",
		json.RawMessage(`{}`), configPath, "")
	if err == nil {
		t.Fatal("expected a no-api-key error, got nil")
	}
	if !strings.Contains(err.Error(), "SATELLITES_API_KEY") || !strings.Contains(err.Error(), "satellites auth") {
		t.Fatalf("error should name both auth paths, got: %v", err)
	}
}
