package cli

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// fakeAS stands in for the satellites-server OAuth Authorization Server
// + exec endpoint. /oauth/authorize immediately 302s back to the
// loopback redirect_uri with a code (no real human login), so the whole
// flow runs headless. The minted JWT and the executor api-key are
// deliberately DIFFERENT strings so the test can prove the JWT is never
// persisted.
func fakeAS(t *testing.T, jwt, apiKey string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/register", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"client_id":"cli-test"}`))
	})
	mux.HandleFunc("/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		redir := q.Get("redirect_uri")
		if redir == "" {
			http.Error(w, "no redirect_uri", http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, redir+"?code=testcode&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"` + jwt + `","token_type":"Bearer","refresh_token":"rt_secret","scope":"satellites"}`))
	})
	mux.HandleFunc("/api/v1/exec/apikey_create", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+jwt {
			http.Error(w, "want JWT bearer, got "+got, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"apikey":"` + apiKey + `","key_id":"apk_mcp_test","role":"executor","project_id":"proj_x"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// browserSim simulates the operator's browser: it GETs the authorize URL
// and follows the AS's redirect onto the loopback callback.
func browserSim(authURL string) error {
	resp, err := http.Get(authURL)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

func TestDoAuthFlow_StoresExecutorKeyNotJWT(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	const jwt = "JWT.JWT.JWT"
	const apiKey = "sk_exec_persisted"
	srv := fakeAS(t, jwt, apiKey)

	cred, err := doAuthFlow(context.Background(), io.Discard, srv.URL, "proj_x", browserSim, 10*time.Second)
	if err != nil {
		t.Fatalf("doAuthFlow: %v", err)
	}
	if cred.Token != apiKey {
		t.Errorf("stored token = %q, want the executor api-key %q", cred.Token, apiKey)
	}
	if cred.Role != "executor" {
		t.Errorf("role = %q, want executor", cred.Role)
	}

	// The credential file must hold the executor key and NEVER the JWT or
	// the refresh token.
	path, _ := credentialsPathForTest(t)
	b, _ := os.ReadFile(path)
	body := string(b)
	if !strings.Contains(body, apiKey) {
		t.Errorf("credential file missing executor key:\n%s", body)
	}
	if strings.Contains(body, jwt) || strings.Contains(body, "rt_secret") {
		t.Errorf("credential file leaked a JWT/refresh token:\n%s", body)
	}
}

func TestDoAuthFlow_StateMismatchRejected(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	srv := fakeAS(t, "j", "k")

	// A browser that returns the wrong state (forged callback).
	badBrowser := func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		redir := u.Query().Get("redirect_uri")
		resp, err := http.Get(redir + "?code=x&state=WRONG")
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		return nil
	}
	if _, err := doAuthFlow(context.Background(), io.Discard, srv.URL, "proj_x", badBrowser, 5*time.Second); err == nil {
		t.Fatal("expected state-mismatch error")
	}
}

func TestDoAuthFlow_RequiresProject(t *testing.T) {
	if _, err := doAuthFlow(context.Background(), io.Discard, "https://x.example", "", nil, time.Second); err == nil {
		t.Fatal("expected error when project_id is empty")
	}
}

func TestDoAuthFlow_RequiresServer(t *testing.T) {
	if _, err := doAuthFlow(context.Background(), io.Discard, "", "proj_x", nil, time.Second); err == nil {
		t.Fatal("expected error when server_url is empty")
	}
}

// credentialsPathForTest mirrors cliconfig.CredentialsPath via the env
// the test set, without importing internal/auth.
func credentialsPathForTest(t *testing.T) (string, error) {
	t.Helper()
	base := os.Getenv("XDG_CONFIG_HOME")
	return base + "/satellites/credentials.toml", nil
}
