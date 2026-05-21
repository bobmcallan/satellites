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
type Config struct {
	Store    *auth.Store
	Sessions *auth.Sessions
	DevMode  bool
	OAuth    auth.OAuthConfig
}

// Build returns the configured root handler.
func Build(cfg Config) http.Handler {
	if cfg.Sessions == nil {
		cfg.Sessions = auth.NewSessions(nil)
	}
	mux := http.NewServeMux()

	// Public routes.
	mux.Handle("/static/", staticHandler())

	// UI auth: cookie-based session via the login form.
	mux.HandleFunc("/login", loginHandler(cfg))
	mux.HandleFunc("/logout", logoutHandler(cfg))

	// Auth-gated UI root. indexHandler itself checks the session cookie
	// and redirects to /login when absent.
	mux.HandleFunc("/", indexHandler(cfg))

	// OAuth routes — own their auth flow (handlers bypass middleware).
	auth.RegisterRoutes(mux, cfg.OAuth)

	// MCP routes — auth-gated via Bearer api-key middleware (agents).
	mcp := mcpserver.HTTPHandler(mcpserver.New())
	mux.Handle("/mcp", cfg.Store.Middleware(mcp))
	mux.Handle("/mcp/", cfg.Store.Middleware(mcp))

	return mux
}
