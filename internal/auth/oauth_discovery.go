package auth

import (
	"encoding/json"
	"net/http"
	"strings"
)

// DefaultOAuthScope is the single scope announced in discovery metadata.
// MCP clients echo it back on /oauth/authorize; agree with v3/v4.
const DefaultOAuthScope = "satellites"

// SchemeAndHost reconstructs the absolute base URL from r, honouring
// X-Forwarded-Proto so requests arriving via Fly's edge TLS resolve to
// https rather than the plaintext scheme the binary itself terminates.
func SchemeAndHost(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// HandleAuthorizationServer serves GET /.well-known/oauth-authorization-server
// (RFC 8414). The /oauth/* endpoints announced here return 404 until the
// follow-up PR lands them — discovery describes the AS contract, not which
// endpoints currently work.
func HandleAuthorizationServer(w http.ResponseWriter, r *http.Request) {
	base := SchemeAndHost(r)
	writeOAuthJSON(w, http.StatusOK, map[string]any{
		"issuer":                                base,
		"authorization_endpoint":                base + "/oauth/authorize",
		"token_endpoint":                        base + "/oauth/token",
		"registration_endpoint":                 base + "/oauth/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                      []string{DefaultOAuthScope},
	}, "public, max-age=3600")
}

// HandleProtectedResource serves GET /.well-known/oauth-protected-resource
// (RFC 9728). Points clients at this server as both the resource and the
// authorizing AS.
func HandleProtectedResource(w http.ResponseWriter, r *http.Request) {
	base := SchemeAndHost(r)
	writeOAuthJSON(w, http.StatusOK, map[string]any{
		"resource":                   base + "/mcp",
		"authorization_servers":      []string{base},
		"scopes_supported":           []string{DefaultOAuthScope},
		"bearer_methods_supported":   []string{"header"},
		"resource_documentation_uri": base + "/mcp",
	}, "public, max-age=3600")
}

func writeOAuthJSON(w http.ResponseWriter, status int, body any, cacheControl string) {
	w.Header().Set("Content-Type", "application/json")
	if cacheControl != "" {
		w.Header().Set("Cache-Control", cacheControl)
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
