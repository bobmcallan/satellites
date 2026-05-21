package server

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/verb"
)

//go:embed templates/*.html static/*
var assets embed.FS

var indexTmpl = template.Must(template.ParseFS(assets, "templates/index.html"))

type indexData struct {
	Title       string
	Version     string
	Commit      string
	BuildTime   string
	DevMode     bool
	DevAdminKey string
	DevUserKey  string
	Endpoints   []endpoint
	UserEmail   string
	FooterName  string
	FooterEmail string
}

// Footer constants (V3/V4 pattern: name, contact, version on every page).
// Operators wanting different contact info should rebuild with these
// overridden via ldflags (-X), same pattern as verb.Version.
var (
	footerName  = "satellites"
	footerEmail = "bobmcallan@gmail.com"
)

func versionString() string {
	if verb.Version == "" {
		return "dev"
	}
	return verb.Version
}

type endpoint struct {
	Method string
	Path   string
	Desc   string
}

func indexHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// http.ServeMux routes "/" as a catch-all for unmatched paths;
		// guard against that so unknown URLs still 404 cleanly.
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}

		// Auth gate: redirect to /login when no valid session cookie.
		// Dev mode still requires login; it just makes the credentials
		// predictable. (V4 behaviour.)
		userID, err := cfg.Sessions.UserID(r)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// Resolve the authenticated user for display. Best-effort —
		// rendering still works without it (UserEmail stays empty).
		var userEmail string
		if cfg.Store != nil && cfg.Store.DB != nil {
			if u, err := cfg.Store.GetUserByID(r.Context(), userID); err == nil && u != nil {
				userEmail = u.Email
			}
		}

		data := indexData{
			Title:       "satellites",
			Version:     verb.Version,
			Commit:      verb.Commit,
			BuildTime:   verb.BuildTime,
			DevMode:     cfg.DevMode,
			UserEmail:   userEmail,
			FooterName:  footerName,
			FooterEmail: footerEmail,
			Endpoints: []endpoint{
				{"POST", "/mcp", "MCP JSON-RPC (Authorization: Bearer required)"},
				{"GET", "/oauth/github/login", "GitHub sign-in (when configured)"},
				{"GET", "/oauth/github/callback", "GitHub OAuth callback"},
				{"GET", "/oauth/google/login", "Google sign-in (when configured)"},
				{"GET", "/oauth/google/callback", "Google OAuth callback"},
				{"GET", "/settings/api-keys", "Issue + revoke MCP api-keys (session-gated)"},
			},
		}
		if cfg.DevMode {
			data.DevAdminKey = auth.DevAdminKey
			data.DevUserKey = auth.DevUserKey
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := indexTmpl.Execute(w, data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// staticHandler serves the embedded CSS/JS assets under /static/.
func staticHandler() http.Handler {
	sub, err := fs.Sub(assets, "static")
	if err != nil {
		// Embed contract violated at build time — panic is appropriate.
		panic(err)
	}
	return http.StripPrefix("/static/", http.FileServer(http.FS(sub)))
}
