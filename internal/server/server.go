// Package server wires the satellites-server HTTP surface — landing page,
// static assets, OAuth handlers, and the auth-gated MCP transport.
//
// Build(cfg) returns a configured http.Handler used by both
// cmd/satellites-server and the integration test harness. Keeping the
// wiring here (not in main.go) is what lets browser-driven integration
// tests boot the same handler stack the operator sees in production.
package server

import (
	"net/http"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/mcpserver"
)

// Config carries the operator-supplied runtime configuration.
//
// OAuth carries credentials + bootstrap policy (admin emails). Providers
// is the constructed ProviderSet derived from OAuth at boot; it's
// passed in so tests can substitute fakes without rebuilding from real
// credentials. OAuthStates is the CSRF state registry shared across
// provider start/callback handlers — long-lived, safe for concurrent
// use.
type Config struct {
	Store       *auth.Store
	Sessions    *auth.Sessions
	DevMode     bool
	OAuth       auth.OAuthConfig
	Providers   *auth.ProviderSet
	OAuthStates *auth.StateStore
	OAuthServer *auth.OAuthServer
}

// Build returns the configured root handler.
func Build(cfg Config) http.Handler {
	if cfg.Sessions == nil {
		cfg.Sessions = auth.NewSessions(nil)
	}
	if cfg.OAuthStates == nil {
		cfg.OAuthStates = auth.NewStateStore(0)
	}
	mux := http.NewServeMux()

	// Public routes.
	mux.Handle("/static/", staticHandler())

	// UI auth: cookie-based session via the login form.
	mux.HandleFunc("/login", loginHandler(cfg))
	mux.HandleFunc("/logout", logoutHandler(cfg))

	// OAuth start + callback routes per enabled provider. Each provider
	// owns its own auth flow; the callback issues the session cookie
	// directly on success.
	registerOAuthRoutes(mux, cfg)

	// MCP OAuth discovery — public per RFC 8414 / RFC 9728.
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", auth.HandleAuthorizationServer)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", auth.HandleProtectedResource)

	// OAuth Authorization Server endpoints (RFC 6749 + 7591 DCR + 7636
	// PKCE). Public — DCR + authorize + token are unauthenticated by
	// design; auth happens inside the flow (PKCE on the code exchange,
	// session cookie on /authorize via the mcp_session_id bridge).
	if cfg.OAuthServer != nil {
		mux.HandleFunc("POST /oauth/register", cfg.OAuthServer.HandleRegister)
		mux.HandleFunc("GET /oauth/authorize", cfg.OAuthServer.HandleAuthorize)
		mux.HandleFunc("POST /oauth/token", cfg.OAuthServer.HandleToken)
	}

	// Session-gated UI surfaces.
	mux.HandleFunc("/", indexHandler(cfg))
	mux.HandleFunc("/docs/mcp", docsMCPHandler(cfg))
	mux.HandleFunc("/settings/api-keys", apiKeysHandler(cfg))
	mux.HandleFunc("/projects", projectsHandler(cfg))

	// MCP routes — auth-gated via Bearer middleware (api-key or JWT).
	mcp := mcpserver.HTTPHandler(mcpserver.New())
	mux.Handle("/mcp", cfg.Store.Middleware(mcp))
	mux.Handle("/mcp/", cfg.Store.Middleware(mcp))

	// CLI ↔ server transport. Same auth pipeline as /mcp; same verbs.
	// POST /api/v1/exec/<verb_name> with the verb's JSON request body.
	mux.Handle("/api/v1/exec/", cfg.Store.Middleware(execHandler()))

	return mux
}
