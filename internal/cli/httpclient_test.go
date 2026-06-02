package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/cliconfig"
)

// TestHTTPDispatch_StampsCorrelationHeadersFromEnv covers the env-to-
// header bridge (sty_7af47a91 AC3): when SATELLITES_* env vars are
// set, the CLI's outbound verb call carries matching X-Satellites-*
// headers so satellites-server's correlation middleware can tag the
// arbor LedgerHandler rows downstream.
func TestHTTPDispatch_StampsCorrelationHeadersFromEnv(t *testing.T) {
	t.Setenv("SATELLITES_RUN_ID", "run_abc")
	t.Setenv("SATELLITES_SESSION_ID", "sess_xyz")
	t.Setenv("SATELLITES_STORY_ID", "sty_1")
	t.Setenv("SATELLITES_PROJECT_ID", "proj_2")
	t.Setenv("SATELLITES_WORKSPACE_ID", "wksp_3")

	var seenHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHeaders = r.Header.Clone()
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cfg := cliconfig.Config{ServerURL: srv.URL, Token: "tok"}
	if _, err := httpDispatch(cfg, "ping", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	cases := map[string]string{
		"X-Satellites-Run-Id":       "run_abc",
		"X-Satellites-Session-Id":   "sess_xyz",
		"X-Satellites-Story-Id":     "sty_1",
		"X-Satellites-Project-Id":   "proj_2",
		"X-Satellites-Workspace-Id": "wksp_3",
	}
	for h, want := range cases {
		if got := seenHeaders.Get(h); got != want {
			t.Errorf("%s: got %q want %q", h, got, want)
		}
	}
}

// TestHTTPDispatch_NoHeadersWhenEnvUnset confirms the bridge is a
// no-op when the env vars are absent — the CLI doesn't fabricate
// correlation ids.
func TestHTTPDispatch_NoHeadersWhenEnvUnset(t *testing.T) {
	// Clear all five vars explicitly so they're empty even if the
	// parent test process inherited them.
	for _, p := range correlationEnvHeaders {
		t.Setenv(p.Env, "")
	}

	var seenHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHeaders = r.Header.Clone()
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cfg := cliconfig.Config{ServerURL: srv.URL, Token: "tok"}
	if _, err := httpDispatch(cfg, "ping", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	for _, p := range correlationEnvHeaders {
		if got := seenHeaders.Get(p.Header); got != "" {
			t.Errorf("%s: should be empty, got %q", p.Header, got)
		}
	}
}

func TestHTTPDispatch_PartialEnvOnlyStampsPresent(t *testing.T) {
	for _, p := range correlationEnvHeaders {
		t.Setenv(p.Env, "")
	}
	t.Setenv("SATELLITES_RUN_ID", "run_only")

	var seenHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cfg := cliconfig.Config{ServerURL: srv.URL, Token: "tok"}
	if _, err := httpDispatch(cfg, "ping", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got := seenHeaders.Get("X-Satellites-Run-Id"); got != "run_only" {
		t.Errorf("X-Satellites-Run-Id: got %q want run_only", got)
	}
	for _, h := range []string{"X-Satellites-Session-Id", "X-Satellites-Story-Id",
		"X-Satellites-Project-Id", "X-Satellites-Workspace-Id"} {
		if got := seenHeaders.Get(h); got != "" {
			t.Errorf("%s: should be empty, got %q", h, got)
		}
	}
	// Trim is honoured — whitespace-only env value shouldn't produce a
	// header.
	t.Setenv("SATELLITES_SESSION_ID", "   ")
	if _, err := httpDispatch(cfg, "ping", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("dispatch (whitespace session): %v", err)
	}
	if got := strings.TrimSpace(seenHeaders.Get("X-Satellites-Session-Id")); got != "" {
		t.Errorf("whitespace session id should be skipped, got %q", got)
	}
}
