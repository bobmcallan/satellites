package server

import (
	"net/http"
	"strings"

	"golang.org/x/oauth2"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/auth"
)

// oauthNextCookie carries a same-site `next` return target across the OAuth
// provider round-trip. The state store mints only opaque CSRF tokens and the
// provider drops our query params, so a short-lived HttpOnly Lax cookie set at
// /login start and read at /callback is how `next` survives the hop.
const oauthNextCookie = "oauth_next"

// registerOAuthRoutes wires /oauth/<provider>/login + /callback for
// every enabled provider in cfg.Providers. The callback path matches
// what's registered in satellites-infra (the GitHub OAuth App points
// at https://satellites-<env>.fly.dev/oauth/github/callback). Disabled
// providers (nil entries) register nothing; callers see 404 from the
// mux itself.
func registerOAuthRoutes(mux *http.ServeMux, cfg Config) {
	if cfg.Providers == nil {
		return
	}
	for _, p := range cfg.Providers.Enabled() {
		provider := p // capture
		mux.HandleFunc("/oauth/"+provider.Name+"/login", oauthStartHandler(cfg, provider))
		mux.HandleFunc("/oauth/"+provider.Name+"/callback", oauthCallbackHandler(cfg, provider))
	}
}

func oauthStartHandler(cfg Config, p *auth.Provider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		state, err := cfg.OAuthStates.Mint()
		if err != nil {
			arbor.ErrorCtx(r.Context(), "oauth: state mint", "provider", p.Name, "err", err)
			http.Error(w, "state mint failed", http.StatusInternalServerError)
			return
		}
		// Carry a same-site return target across the provider round-trip.
		if next := safeNext(r.URL.Query().Get("next")); next != "/" {
			http.SetCookie(w, &http.Cookie{
				Name:     oauthNextCookie,
				Value:    next,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
				MaxAge:   600,
			})
		}
		url := p.OAuth2.AuthCodeURL(state, oauth2.AccessTypeOnline)
		arbor.InfoCtx(r.Context(), "oauth: start", "provider", p.Name)
		http.Redirect(w, r, url, http.StatusSeeOther)
	}
}

func oauthCallbackHandler(cfg Config, p *auth.Provider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		q := r.URL.Query()
		if errCode := q.Get("error"); errCode != "" {
			arbor.WarnCtx(r.Context(), "oauth: provider returned error", "provider", p.Name, "error", errCode)
			http.Error(w, "oauth provider error: "+errCode, http.StatusBadRequest)
			return
		}
		state := q.Get("state")
		if err := cfg.OAuthStates.Consume(state); err != nil {
			arbor.WarnCtx(r.Context(), "oauth: invalid state", "provider", p.Name, "err", err)
			http.Error(w, "invalid state", http.StatusBadRequest)
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			return
		}

		token, err := p.OAuth2.Exchange(r.Context(), code)
		if err != nil {
			arbor.WarnCtx(r.Context(), "oauth: exchange failed", "provider", p.Name, "err", err)
			http.Error(w, "token exchange failed", http.StatusBadGateway)
			return
		}
		info, err := p.FetchInfo(r.Context(), token)
		if err != nil {
			arbor.WarnCtx(r.Context(), "oauth: userinfo failed", "provider", p.Name, "err", err)
			http.Error(w, "userinfo fetch failed", http.StatusBadGateway)
			return
		}
		if strings.TrimSpace(info.Email) == "" || strings.TrimSpace(info.Sub) == "" {
			arbor.WarnCtx(r.Context(), "oauth: incomplete userinfo", "provider", p.Name)
			http.Error(w, "incomplete userinfo", http.StatusBadRequest)
			return
		}

		u, err := cfg.Store.UpsertOAuthUser(r.Context(), p.Name, info.Sub, info.Email, info.DisplayName, cfg.OAuth.AdminEmails)
		if err != nil {
			arbor.ErrorCtx(r.Context(), "oauth: upsert user", "provider", p.Name, "err", err)
			http.Error(w, "could not link account", http.StatusInternalServerError)
			return
		}

		// Provision per-user state (personal workspace). Best-effort —
		// a failure must not block login.
		if cfg.ProvisionLogin != nil {
			if err := cfg.ProvisionLogin(r.Context(), u.ID, u.Email, u.DisplayName); err != nil {
				arbor.ErrorCtx(r.Context(), "oauth: provision login", "user_id", u.ID, "err", err)
			}
		}

		cfg.Sessions.Issue(w, u.ID)
		arbor.InfoCtx(r.Context(), "oauth: login", "provider", p.Name, "user_id", u.ID, "role", string(u.Role))
		// Resolve + clear the same-site return target carried from /login.
		next := "/"
		if c, err := r.Cookie(oauthNextCookie); err == nil {
			next = safeNext(c.Value)
			http.SetCookie(w, &http.Cookie{Name: oauthNextCookie, Value: "", Path: "/", MaxAge: -1})
		}
		if dest := completeMCPSessionIfPresent(w, r, cfg, u.ID); dest != "" {
			http.Redirect(w, r, dest, http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, next, http.StatusSeeOther)
	}
}
