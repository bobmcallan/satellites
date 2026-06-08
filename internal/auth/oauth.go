package auth

import (
	"context"
	"strings"

	"golang.org/x/oauth2"
)

// ProviderUserInfo is the minimal identity the auth layer needs from an
// OAuth provider — provider-stable subject id (for the (oauth_provider,
// oauth_sub) unique index on users), a verified email, and a display
// name. Providers map their own payloads into this shape via FetchInfo.
type ProviderUserInfo struct {
	Sub         string
	Email       string
	DisplayName string
}

// Provider ties an oauth2.Config + a provider-specific userinfo fetcher
// to a human-readable name used in routes + users.oauth_provider rows.
type Provider struct {
	Name      string
	OAuth2    *oauth2.Config
	FetchInfo func(ctx context.Context, token *oauth2.Token) (ProviderUserInfo, error)
}

// ProviderSet holds the enabled providers. Missing provider configs
// (e.g. GOOGLE_OAUTH_CLIENT_ID=="") leave the provider nil, which
// disables its routes.
type ProviderSet struct {
	GitHub *Provider
	Google *Provider
}

// Enabled returns the list of active providers in stable render order
// (Google first, GitHub second). Order matters: the login template
// renders buttons in this order — Google is the preferred sign-in
// path for operators.
func (p *ProviderSet) Enabled() []*Provider {
	if p == nil {
		return nil
	}
	out := make([]*Provider, 0, 2)
	if p.Google != nil {
		out = append(out, p.Google)
	}
	if p.GitHub != nil {
		out = append(out, p.GitHub)
	}
	return out
}

// OAuthConfig captures the operator-supplied provider configuration.
// AdminEmails contains the comma-split, lowercased list of operator
// emails promoted to RoleAdmin on first OAuth sign-in — the prod
// bootstrap mechanism.
type OAuthConfig struct {
	GitHubClientID     string
	GitHubClientSecret string
	GoogleClientID     string
	GoogleClientSecret string
	RedirectBaseURL    string
	AdminEmails        []string
}

// BuildProviderSet constructs a ProviderSet from OAuthConfig. Providers
// missing any of (client_id, client_secret, redirect_base_url) are
// disabled (nil).
func BuildProviderSet(cfg OAuthConfig) *ProviderSet {
	base := strings.TrimRight(strings.TrimSpace(cfg.RedirectBaseURL), "/")
	set := &ProviderSet{}
	if cfg.GitHubClientID != "" && cfg.GitHubClientSecret != "" && base != "" {
		set.GitHub = &Provider{
			Name: "github",
			OAuth2: &oauth2.Config{
				ClientID:     cfg.GitHubClientID,
				ClientSecret: cfg.GitHubClientSecret,
				RedirectURL:  base + "/oauth/github/callback",
				Scopes:       []string{"read:user", "user:email"},
				Endpoint: oauth2.Endpoint{
					AuthURL:  "https://github.com/login/oauth/authorize",
					TokenURL: "https://github.com/login/oauth/access_token",
				},
			},
			FetchInfo: fetchGitHubUserInfo,
		}
	}
	if cfg.GoogleClientID != "" && cfg.GoogleClientSecret != "" && base != "" {
		set.Google = &Provider{
			Name: "google",
			OAuth2: &oauth2.Config{
				ClientID:     cfg.GoogleClientID,
				ClientSecret: cfg.GoogleClientSecret,
				RedirectURL:  base + "/oauth/google/callback",
				Scopes:       []string{"openid", "email", "profile"},
				Endpoint: oauth2.Endpoint{
					AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
					TokenURL: "https://oauth2.googleapis.com/token",
				},
			},
			FetchInfo: fetchGoogleUserInfo,
		}
	}
	return set
}

// ParseAdminEmails splits, trims, lowercases a comma-separated
// SATELLITES_ADMIN_EMAILS value. Empty input returns nil.
func ParseAdminEmails(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// StateStore is the OAuth CSRF state registry. /oauth/<provider>/login
// mints a state and writes it; /oauth/<provider>/callback consumes it.
//
// The only implementation is PGStateStore, so state survives Fly machine
// recycle, rolling deploys, and scale-out across instances — the in-memory
// store that caused the "invalid state" outage on rolling deploys was
// removed (sty_38effee9). A test that genuinely needs an in-process fake
// declares a small stub against this interface.
type StateStore interface {
	Mint() (string, error)
	Consume(id string) error
}
