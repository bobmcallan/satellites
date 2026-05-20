package auth

import (
	"fmt"
	"net/http"
)

// OAuthConfig captures the operator-supplied provider configuration.
// Currently GitHub-only; additional providers land in follow-up stories.
type OAuthConfig struct {
	GitHubClientID     string
	GitHubClientSecret string
}

// Configured returns true when at least one provider is fully set.
func (c OAuthConfig) Configured() bool {
	return c.GitHubClientID != "" && c.GitHubClientSecret != ""
}

// RegisterRoutes wires OAuth login + callback handlers onto the mux.
//
// Partial delivery per sty_9b3e355c: handler routes exist and respond
// with structured status codes, but full provider integration (code
// exchange, user mint, key issuance, redirect to caller's localhost
// listener) lands in a follow-up. For now, operators authenticate via
// `satellites init --api-key`.
func RegisterRoutes(mux *http.ServeMux, cfg OAuthConfig) {
	mux.HandleFunc("/oauth/github/login", func(w http.ResponseWriter, r *http.Request) {
		if !cfg.Configured() {
			http.Error(w,
				"OAuth not configured — set GITHUB_OAUTH_CLIENT_ID + GITHUB_OAUTH_CLIENT_SECRET",
				http.StatusServiceUnavailable)
			return
		}
		http.Error(w,
			"OAuth login flow not yet wired — use --api-key for now",
			http.StatusNotImplemented)
	})

	mux.HandleFunc("/oauth/github/callback", func(w http.ResponseWriter, r *http.Request) {
		if !cfg.Configured() {
			http.Error(w, "OAuth not configured", http.StatusServiceUnavailable)
			return
		}
		http.Error(w,
			fmt.Sprintf("OAuth callback not yet wired (received: %s)", r.URL.RawQuery),
			http.StatusNotImplemented)
	})
}
