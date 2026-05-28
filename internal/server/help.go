package server

import (
	"html/template"
	"net/http"
)

var helpTmpl = template.Must(template.ParseFS(assets,
	"templates/help.html", "templates/_user_menu.html"))

type helpPageData struct {
	Title       string
	UserEmail   string
	UserName    string
	UserAvatar  string
	ActiveNav   string
	FooterName  string
	FooterEmail string
	Version     string
}

// helpHandler serves GET /help. Public — no session gate, same as
// /changelog. The page is static role-model documentation rendered
// straight from the template; there is no store behind it. The user
// menu degrades to a sign-in chip when no session cookie is present.
func helpHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/help" {
			http.NotFound(w, r)
			return
		}

		data := helpPageData{
			Title:       "help · satellites",
			ActiveNav:   "help",
			FooterName:  footerName,
			FooterEmail: footerEmail,
			Version:     versionString(),
		}
		// Best-effort user resolution for the nav avatar — same pattern
		// as changelogHandler.
		if cfg.Sessions != nil {
			if userID, err := cfg.Sessions.UserID(r); err == nil && cfg.Store != nil && cfg.Store.DB != nil {
				if u, err := cfg.Store.GetUserByID(r.Context(), userID); err == nil && u != nil {
					data.UserEmail = u.Email
					data.UserName = u.DisplayName
					data.UserAvatar = avatarLetter(u.DisplayName, u.Email)
				}
			}
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := helpTmpl.Execute(w, data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}
