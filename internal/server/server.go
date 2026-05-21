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
	Store   *auth.Store
	DevMode bool
	OAuth   auth.OAuthConfig
}

// Build returns the configured root handler.
func Build(cfg Config) http.Handler {
	mux := http.NewServeMux()

	// Public routes — no auth.
	mux.HandleFunc("/", indexHandler(cfg))
	mux.Handle("/static/", staticHandler())

	// OAuth routes — own their auth flow (handlers bypass middleware).
	auth.RegisterRoutes(mux, cfg.OAuth)

	// MCP routes — auth-gated via Bearer token middleware.
	mcp := mcpserver.HTTPHandler(mcpserver.New())
	mux.Handle("/mcp", cfg.Store.Middleware(mcp))
	mux.Handle("/mcp/", cfg.Store.Middleware(mcp))

	return mux
}
