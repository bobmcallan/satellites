package server

import (
	"html/template"
	"net/http"

	"github.com/bobmcallan/satellites/internal/auth"
)

var docsMCPTmpl = template.Must(template.ParseFS(assets, "templates/docs_mcp.html", "templates/_user_menu.html"))

type docsMCPData struct {
	Title       string
	DevMode     bool
	DevAdminKey string
	DevUserKey  string
	UserEmail   string
	UserName    string
	UserAvatar  string
	ExampleURL  string
	ExampleKey  string
	FooterName  string
	FooterEmail string
	Version     string
}

func docsMCPHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := cfg.Sessions.UserID(r)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		var userEmail, userName, userAvatar string
		if cfg.Store != nil && cfg.Store.DB != nil {
			if u, err := cfg.Store.GetUserByID(r.Context(), userID); err == nil && u != nil {
				userEmail = u.Email
				userName = u.DisplayName
				userAvatar = avatarLetter(u.DisplayName, u.Email)
			}
		}

		data := docsMCPData{
			Title:       "mcp · satellites",
			DevMode:     cfg.DevMode,
			UserEmail:   userEmail,
			UserName:    userName,
			UserAvatar:  userAvatar,
			ExampleURL:  "http://localhost:8080",
			ExampleKey:  "<your-api-key>",
			FooterName:  footerName,
			FooterEmail: footerEmail,
			Version:     versionString(),
		}
		if cfg.DevMode {
			data.DevAdminKey = auth.DevAdminKey
			data.DevUserKey = auth.DevUserKey
			data.ExampleKey = auth.DevAdminKey
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := docsMCPTmpl.Execute(w, data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}
