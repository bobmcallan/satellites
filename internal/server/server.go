// Package server wires the satellites-server HTTP surface — landing page,
// static assets, OAuth handlers, and the auth-gated MCP transport.
//
// Build(cfg) returns a configured http.Handler used by both
// cmd/satellites-server and the integration test harness. Keeping the
// wiring here (not in main.go) is what lets browser-driven integration
// tests boot the same handler stack the operator sees in production.
package server

import (
	"context"
	"net/http"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/live"
	"github.com/bobmcallan/satellites/internal/mcpserver"
)

// Config carries the operator-supplied runtime configuration.
//
// OAuth carries credentials + bootstrap policy (admin emails). Providers
// is the constructed ProviderSet derived from OAuth at boot; it's
// passed in so tests can substitute fakes without rebuilding from real
// credentials. OAuthStates is the CSRF state registry shared across
// provider start/callback handlers — long-lived, safe for concurrent
// use. It is caller-supplied: production wires auth.NewPGStateStore so
// state survives Fly machine recycle / rolling deploys (sty_38effee9).
// OAuth routes register only when Providers is set, so a caller that
// uses neither may leave it nil.
type Config struct {
	Store       *auth.Store
	Sessions    *auth.Sessions
	DevMode     bool
	OAuth       auth.OAuthConfig
	Providers   *auth.ProviderSet
	OAuthStates auth.StateStore
	OAuthServer *auth.OAuthServer
	// Live is the SSE trigger bus hub (sty_b6e39eb8). When non-nil (and
	// LiveScope is set) the session-gated GET /events stream is registered;
	// nil (tests / CLI without a Postgres listener) leaves it off and the rest
	// of the surface builds.
	Live *live.Hub
	// LiveScope resolves a user's /events authorization scope. Injected from
	// main (which may import substrate stores) so this transport package stays
	// free of substrate-domain imports per the layering guard.
	LiveScope LiveScoper
	// ProvisionLogin runs on every successful login (OAuth or password) to
	// provision per-user state — the personal workspace + claiming pending
	// email invitations (epic:user-admin, sty_480dba9b). Injected from main so
	// this package keeps clear of substrate stores; nil is a no-op.
	ProvisionLogin LoginProvisioner
	// StoreBlob persists a binary upload (sty_59652a7d) and returns its
	// reference. Injected from main so this transport package imports no blob
	// substrate store (layering guard); nil disables the
	// POST /projects/{id}/blobs upload route.
	StoreBlob StoreBlobFunc
	// GetBlob fetches a stored blob's bytes for the GET
	// /projects/{id}/blobs/{blob} download route (sty_52c2393f). Injected from
	// main; nil disables the download route.
	GetBlob GetBlobFunc
}

// LiveScoper resolves the SSE topic-scope for a session user — admins see
// every topic, others only their workspaces' topics. See cmd/satellites-server.
type LiveScoper func(ctx context.Context, userID string) (live.Scope, error)

// LoginProvisioner provisions per-user state on every successful login (OAuth
// or password): the user's personal workspace, and claiming any pending email
// invitations for their verified email. Best-effort: a failure is logged, not
// surfaced to the user, so a provisioning hiccup never blocks sign-in.
type LoginProvisioner func(ctx context.Context, userID, email, displayName string) error

// Build returns the configured root handler.
func Build(cfg Config) http.Handler {
	if cfg.Sessions == nil {
		cfg.Sessions = auth.NewSessions(nil)
	}
	mux := http.NewServeMux()

	// Public routes.
	mux.Handle("/static/", staticHandler())
	mux.HandleFunc("/favicon.ico", faviconHandler())

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
	mux.HandleFunc("/settings/people", adminPeopleHandler(cfg))
	mux.HandleFunc("/projects", projectsHandler(cfg))
	// Live story-list refetch fragment (sty_8f69be8b) — more specific than
	// /projects/ so it wins under Go 1.22 pattern precedence.
	mux.HandleFunc("GET /projects/{id}/stories.fragment", storyFragmentHandler(cfg))
	// Binary ingestion (sty_59652a7d): multipart upload to a project the caller
	// can access, behind the same Bearer middleware as /mcp. More specific than
	// /projects/ so it wins under Go 1.22 pattern precedence. Registered only
	// when a blob store is wired.
	if cfg.StoreBlob != nil {
		mux.Handle("POST /projects/{id}/blobs", correlationMiddleware(cfg.Store.Middleware(http.HandlerFunc(blobUploadHandler(cfg)))))
	}
	if cfg.GetBlob != nil {
		mux.Handle("GET /projects/{id}/blobs/{blob}", correlationMiddleware(cfg.Store.Middleware(http.HandlerFunc(blobDownloadHandler(cfg)))))
	}
	mux.HandleFunc("/projects/", projectDetailHandler(cfg))
	// Live trace fragment (sty_96cc0ade) — more specific than /stories/ so it
	// wins under Go 1.22 pattern precedence. Replaces the retired per-story SSE
	// (/api/stories/{id}/events); the QA view now refetches off the shared bus.
	mux.HandleFunc("GET /stories/{id}/trace.fragment", storyTraceFragmentHandler(cfg))
	// Ledger fragment (sty_762730ad) — lazy-load target for the project-detail
	// inline panel's Ledger/Log tab. More specific than /stories/ so it wins.
	mux.HandleFunc("GET /stories/{id}/ledger.fragment", storyLedgerFragmentHandler(cfg))
	mux.HandleFunc("/stories/", storyDetailHandler(cfg))
	mux.HandleFunc("/ledger", ledgerHandler(cfg))

	// SSE trigger bus (sty_b6e39eb8): one app-wide, per-user-scoped,
	// trigger-only event stream. Registered only when a hub is wired (a
	// Postgres listener is running); absent that, the surface still builds.
	if cfg.Live != nil && cfg.LiveScope != nil {
		mux.HandleFunc("GET /events", liveEventsHandler(cfg))
		// Session-gated diagnostic: in-memory bus counters as JSON
		// (sty_2d01bb70).
		mux.HandleFunc("GET /events/stats", liveStatsHandler(cfg))
	}

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
