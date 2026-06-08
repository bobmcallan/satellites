package server

import (
	"errors"
	"html/template"
	"net/http"

	"github.com/bobmcallan/satellites/internal/arbor"
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

	// Same post-auth provisioning as the OAuth path (sty_480dba9b): the
	// satellites account is the email, so a basic-auth login also provisions
	// the personal workspace and claims pending invitations. Best-effort —
	// never block login.
	if cfg.ProvisionLogin != nil {
		if err := cfg.ProvisionLogin(r.Context(), u.ID, u.Email, u.DisplayName); err != nil {
			arbor.ErrorCtx(r.Context(), "login: provision", "user_id", u.ID, "err", err)
		}
	}

	cfg.Sessions.Issue(w, u.ID)
	if dest := completeMCPSessionIfPresent(w, r, cfg, u.ID); dest != "" {
		http.Redirect(w, r, dest, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// completeMCPSessionIfPresent checks for the mcp_session_id cookie
// dropped by OAuthServer.setSessionCookieAndRedirect. When present,
// completes the OAuth flow (mints a code, deletes the session) and
// returns the client redirect URL with ?code=…&state=…. Returns "" when
// no MCP session is bound or completion failed — caller falls back to
// the regular post-login destination.
func completeMCPSessionIfPresent(w http.ResponseWriter, r *http.Request, cfg Config, userID string) string {
	c, err := r.Cookie("mcp_session_id")
	if err != nil || c.Value == "" {
		return ""
	}
	if cfg.OAuthServer == nil {
		return ""
	}
	redirectURL, err := cfg.OAuthServer.CompleteAuthorization(r.Context(), c.Value, userID)
	if err != nil {
		return ""
	}
	// Clear the bridge cookie.
	http.SetCookie(w, &http.Cookie{
		Name:   "mcp_session_id",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
	return redirectURL
}

func logoutHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg.Sessions.Clear(w)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	}
}
