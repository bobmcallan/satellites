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
