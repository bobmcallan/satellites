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

		data := indexData{
			Title:     "satellites",
			Version:   verb.Version,
			Commit:    verb.Commit,
			BuildTime: verb.BuildTime,
			DevMode:   cfg.DevMode,
			Endpoints: []endpoint{
				{"POST", "/mcp", "MCP JSON-RPC (Authorization: Bearer required)"},
				{"GET", "/oauth/github/login", "OAuth login redirect (when configured)"},
				{"GET", "/oauth/github/callback", "OAuth callback"},
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
