package server

import (
	"errors"
	"html/template"
	"net/http"

	"github.com/bobmcallan/satellites/internal/auth"
)

var loginTmpl = template.Must(template.ParseFS(assets, "templates/login.html"))

type loginData struct {
	Title       string
	Error       string
	DevMode     bool
	DevAdmin    devCred
	DevUser     devCred
	Providers   []providerButton
	FooterName  string
	FooterEmail string
	Version     string
}

type devCred struct {
	Email    string
	Password string
}

type providerButton struct {
	Name  string // "github", "google" — used in data-action + start URL
	Label string // "GitHub", "Google" — display text
}

func loginHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			renderLogin(w, cfg, "")
		case http.MethodPost:
			handleLoginPost(w, r, cfg)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func renderLogin(w http.ResponseWriter, cfg Config, errMsg string) {
	data := loginData{
		Title:       "login · satellites",
		Error:       errMsg,
		DevMode:     cfg.DevMode,
		Providers:   enabledProviderButtons(cfg),
		FooterName:  footerName,
		FooterEmail: footerEmail,
		Version:     versionString(),
	}
	if cfg.DevMode {
		data.DevAdmin = devCred{auth.DevAdminEmail, auth.DevAdminPassword}
		data.DevUser = devCred{auth.DevUserEmail, auth.DevUserPassword}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if errMsg != "" {
		w.WriteHeader(http.StatusUnauthorized)
	}
	if err := loginTmpl.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// enabledProviderButtons turns cfg.Providers into the display list the
// template renders. Order tracks ProviderSet.Enabled.
func enabledProviderButtons(cfg Config) []providerButton {
	if cfg.Providers == nil {
		return nil
	}
	labels := map[string]string{
		"github": "GitHub",
		"google": "Google",
	}
	enabled := cfg.Providers.Enabled()
	out := make([]providerButton, 0, len(enabled))
	for _, p := range enabled {
		label, ok := labels[p.Name]
		if !ok {
			label = p.Name
		}
		out = append(out, providerButton{Name: p.Name, Label: label})
	}
	return out
}

func handleLoginPost(w http.ResponseWriter, r *http.Request, cfg Config) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	email := r.FormValue("email")
	password := r.FormValue("password")

	u, err := cfg.Store.VerifyPassword(r.Context(), email, password)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidPassword) {
			renderLogin(w, cfg, "Invalid email or password.")
			return
		}
		http.Error(w, "login error", http.StatusInternalServerError)
		return
	}

	cfg.Sessions.Issue(w, u.ID)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func logoutHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Sessions.Clear(w)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	}
}
