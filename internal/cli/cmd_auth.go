// `satellites auth` — human-in-the-loop client login.
//
// Realises the auth_bootstrap.kind: auth_login flow (see the header of
// internal/verb/apikey.go). The CLI runs a loopback OAuth 2.1
// authorization-code + PKCE flow against the satellites-server's
// Authorization Server: it opens a browser URL, the operator
// authenticates + approves, and the CLI is provisioned with a durable
// EXECUTOR-role api-key stored in the user-level credential store
// (cliconfig.SaveCredential).
//
// The OAuth access token (a JWT) is used exactly once — to mint the
// executor key via apikey_create — and then discarded. It is never
// persisted: a JWT bypasses the reviewer role gate, so persisting it
// would make the agent's channel reviewer-capable (sty_c2ecbb86).

package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/cliconfig"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/spf13/cobra"
)

var authHTTPClient = &http.Client{Timeout: 30 * time.Second}

func init() {
	var (
		configArg  string
		serverArg  string
		projectArg string
		noBrowser  bool
		timeout    time.Duration
	)

	auth := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate the client (human-in-the-loop browser login)",
		Long: `auth runs a browser login against the satellites-server and stores a
durable executor-role api-key in the user-level credential store
($XDG_CONFIG_HOME/satellites/credentials.toml). Running 'satellites auth'
with no subcommand is equivalent to 'satellites auth login'.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAuthLogin(cmd, configArg, serverArg, projectArg, noBrowser, timeout)
		},
	}
	auth.PersistentFlags().StringVar(&configArg, "config", "", "Path to satellites.toml (overrides $SATELLITES_CONFIG / .satellites/satellites.toml walk-up).")
	auth.PersistentFlags().StringVar(&serverArg, "server", "", "Server URL (overrides satellites.toml server_url).")
	auth.PersistentFlags().StringVar(&projectArg, "project", "", "Project id to mint the key against (overrides satellites.toml project_id).")
	auth.PersistentFlags().BoolVar(&noBrowser, "no-browser", false, "Print the auth URL instead of opening a browser.")
	auth.PersistentFlags().DurationVar(&timeout, "timeout", 3*time.Minute, "How long to wait for the browser approval.")

	login := &cobra.Command{
		Use:   "login",
		Short: "Authenticate and store an executor api-key",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAuthLogin(cmd, configArg, serverArg, projectArg, noBrowser, timeout)
		},
	}
	auth.AddCommand(login)

	register(auth)
}

func runAuthLogin(cmd *cobra.Command, configArg, serverArg, projectArg string, noBrowser bool, timeout time.Duration) error {
	cfg, _, err := cliconfig.Load(configArg)
	if err != nil && !errors.Is(err, cliconfig.ErrNotFound) {
		return err
	}
	serverURL := strings.TrimSpace(serverArg)
	if serverURL == "" {
		serverURL = cfg.ServerURL
	}
	projectID := strings.TrimSpace(projectArg)
	if projectID == "" {
		projectID = cfg.ProjectID
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	out := cmd.OutOrStdout()

	var opener browserOpener
	if !noBrowser {
		opener = openBrowser
	}

	cred, err := doAuthFlow(ctx, out, serverURL, projectID, opener, timeout)
	if err != nil {
		return err
	}
	path, _ := cliconfig.CredentialsPath()
	fmt.Fprintf(out, "Authenticated. Stored %s api-key %s for %s\n  → %s\n", cred.Role, cred.KeyID, cred.ServerURL, path)
	return nil
}

type browserOpener func(authURL string) error

// doAuthFlow runs the loopback authorization-code + PKCE flow end to
// end and persists the resulting EXECUTOR api-key. The bootstrap JWT is
// used once (to mint) and never persisted. Factored out of the cobra
// command so it can be driven by tests against an httptest server.
func doAuthFlow(ctx context.Context, out io.Writer, serverURL, projectID string, open browserOpener, timeout time.Duration) (cliconfig.Credential, error) {
	serverURL = strings.TrimRight(strings.TrimSpace(serverURL), "/")
	if serverURL == "" {
		return cliconfig.Credential{}, fmt.Errorf("auth: server_url not set (configure satellites.toml or pass --server)")
	}
	if strings.TrimSpace(projectID) == "" {
		return cliconfig.Credential{}, fmt.Errorf("auth: project_id not set (configure satellites.toml or pass --project; run 'satellites project match' to resolve)")
	}

	// 1. Loopback listener on an ephemeral port — the redirect target.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return cliconfig.Credential{}, fmt.Errorf("auth: open loopback listener: %w", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	// 2. Per-run Dynamic Client Registration for this redirect_uri.
	clientID, err := registerClient(ctx, serverURL, redirectURI)
	if err != nil {
		return cliconfig.Credential{}, err
	}

	// 3. PKCE (S256) + CSRF state.
	verifier, err := randomURLToken(32)
	if err != nil {
		return cliconfig.Credential{}, err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	state, err := randomURLToken(16)
	if err != nil {
		return cliconfig.Credential{}, err
	}

	// 4. Loopback callback handler.
	type cbResult struct {
		code string
		err  error
	}
	resultCh := make(chan cbResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case q.Get("error") != "":
			writeCallbackPage(w, "Authentication failed: "+q.Get("error"))
			resultCh <- cbResult{err: fmt.Errorf("auth: authorization error: %s", q.Get("error"))}
		case q.Get("state") != state:
			writeCallbackPage(w, "Authentication failed: state mismatch.")
			resultCh <- cbResult{err: fmt.Errorf("auth: state mismatch (possible CSRF)")}
		case q.Get("code") == "":
			writeCallbackPage(w, "Authentication failed: no authorization code.")
			resultCh <- cbResult{err: fmt.Errorf("auth: no authorization code in callback")}
		default:
			writeCallbackPage(w, "Authentication complete — you may close this tab.")
			resultCh <- cbResult{code: q.Get("code")}
		}
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	// 5. Send the operator to the authorize URL.
	authURL := buildAuthorizeURL(serverURL, clientID, redirectURI, challenge, state)
	fmt.Fprintf(out, "Open this URL to authenticate:\n\n  %s\n\n", authURL)
	if open != nil {
		if err := open(authURL); err != nil {
			fmt.Fprintf(out, "(could not open a browser automatically: %v)\n", err)
		}
	}

	// 6. Wait for the browser to land the code on the loopback.
	var code string
	select {
	case res := <-resultCh:
		if res.err != nil {
			return cliconfig.Credential{}, res.err
		}
		code = res.code
	case <-time.After(timeout):
		return cliconfig.Credential{}, fmt.Errorf("auth: timed out after %s waiting for browser approval", timeout)
	case <-ctx.Done():
		return cliconfig.Credential{}, ctx.Err()
	}

	// 7. Exchange the code for a single-use JWT.
	jwt, err := exchangeCode(ctx, serverURL, clientID, redirectURI, code, verifier)
	if err != nil {
		return cliconfig.Credential{}, err
	}

	// 8. Use the JWT ONCE to mint an executor api-key; discard the JWT.
	cred, err := mintExecutorKey(ctx, serverURL, projectID, jwt)
	if err != nil {
		return cliconfig.Credential{}, err
	}

	// 9. Persist the executor key (never the JWT).
	if err := cliconfig.SaveCredential(cred); err != nil {
		return cliconfig.Credential{}, fmt.Errorf("auth: store credential: %w", err)
	}
	return cred, nil
}

func registerClient(ctx context.Context, serverURL, redirectURI string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"client_name":                "satellites-cli",
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/oauth/register", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := authHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("auth: register client: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("auth: register client: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	var out struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil || out.ClientID == "" {
		return "", fmt.Errorf("auth: register client: bad response: %s", strings.TrimSpace(string(respBody)))
	}
	return out.ClientID, nil
}

func buildAuthorizeURL(serverURL, clientID, redirectURI, challenge, state string) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)
	q.Set("scope", "satellites")
	return serverURL + "/oauth/authorize?" + q.Encode()
}

func exchangeCode(ctx context.Context, serverURL, clientID, redirectURI, code, verifier string) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", clientID)
	form.Set("redirect_uri", redirectURI)
	form.Set("code_verifier", verifier)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := authHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("auth: token exchange: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("auth: token exchange: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil || out.AccessToken == "" {
		return "", fmt.Errorf("auth: token exchange: no access_token in response")
	}
	return out.AccessToken, nil
}

// mintExecutorKey calls apikey_create over HTTP under the one-time JWT.
// No role is requested, so the server mints an executor key.
func mintExecutorKey(ctx context.Context, serverURL, projectID, jwt string) (cliconfig.Credential, error) {
	reqBody, _ := json.Marshal(verb.APIKeyCreateRequest{ProjectID: projectID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/api/v1/exec/apikey_create", bytes.NewReader(reqBody))
	if err != nil {
		return cliconfig.Credential{}, err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Content-Type", "application/json")
	resp, err := authHTTPClient.Do(req)
	if err != nil {
		return cliconfig.Credential{}, fmt.Errorf("auth: mint executor key: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return cliconfig.Credential{}, fmt.Errorf("auth: mint executor key: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	var out verb.APIKeyCreateResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return cliconfig.Credential{}, fmt.Errorf("auth: mint executor key: bad response: %w", err)
	}
	if strings.TrimSpace(out.APIKey) == "" {
		return cliconfig.Credential{}, fmt.Errorf("auth: mint executor key: empty key returned")
	}
	return cliconfig.Credential{
		ServerURL: serverURL,
		Token:     out.APIKey,
		KeyID:     out.KeyID,
		Role:      out.Role,
	}, nil
}

func randomURLToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func writeCallbackPage(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, "<!doctype html><html><body style=\"font-family:sans-serif;padding:2rem\"><p>%s</p></body></html>", html.EscapeString(msg))
}

func openBrowser(target string) error {
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name, args = "open", []string{target}
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler", target}
	default:
		name, args = "xdg-open", []string{target}
	}
	return exec.Command(name, args...).Start()
}
