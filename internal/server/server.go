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
	OAuthStates auth.StateStore
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

	// Public read-only page — render the changelog table without a
	// session gate (matches /login as the other unauthenticated surface).
	mux.HandleFunc("/changelog", changelogHandler(cfg))

	// Public role-model documentation. Static — no store, no session
	// gate; same unauthenticated surface as /changelog.
	mux.HandleFunc("/help", helpHandler(cfg))

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
	mux.HandleFunc("/settings/system-kv", systemKVHandler(cfg))
	mux.HandleFunc("/projects", projectsHandler(cfg))
	mux.HandleFunc("/projects/", projectDetailHandler(cfg))
	mux.HandleFunc("/stories/", storyDetailHandler(cfg))
	// More-specific than /api/stories/ (storyStatusHandler) so the SSE stream
	// wins for the events sub-path under Go 1.22 pattern precedence.
	mux.HandleFunc("GET /api/stories/{id}/events", storyEventsHandler(cfg))
	mux.HandleFunc("/api/stories/", storyStatusHandler(cfg))
	mux.HandleFunc("/ledger", ledgerHandler(cfg))

	// MCP routes — auth-gated via Bearer middleware (api-key or JWT).
	// correlationMiddleware lifts X-Satellites-* headers onto request
	// context so arbor's LedgerHandler can tag log events with the
	// caller's run/session/story/project/workspace ids. It wraps the
	// auth middleware so the ids are visible even on unauthenticated
	// requests (auth failures should still ledger if the run was named).
	mcp := mcpserver.HTTPHandler(mcpserver.New())
	mux.Handle("/mcp", correlationMiddleware(cfg.Store.Middleware(mcp)))
	mux.Handle("/mcp/", correlationMiddleware(cfg.Store.Middleware(mcp)))

	// CLI ↔ server transport. Same auth pipeline as /mcp; same verbs.
	// POST /api/v1/exec/<verb_name> with the verb's JSON request body.
	mux.Handle("/api/v1/exec/", correlationMiddleware(cfg.Store.Middleware(execHandler())))

	return mux
}
