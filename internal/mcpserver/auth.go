package mcpserver

import (
	"context"
	"net/http"
	"strings"

	"github.com/ternarybob/arbor"

	"github.com/bobmcallan/satellites/internal/auth"
)

type ctxKey int

const userKey ctxKey = iota

// CallerIdentity is what handlers read from ctx: the resolved user's email
// (for sessions) or a synthetic api-key id. UserID is the stable opaque
// identifier used by project/document ownership — sess.UserID for session
// callers, the literal "apikey" for API-key callers.
type CallerIdentity struct {
	Email  string
	UserID string
	Source string // "session" | "apikey"
	// GlobalAdmin is set when the resolved user has the persisted flag
	// or matches SATELLITES_GLOBAL_ADMIN_EMAILS. Used by handlers that
	// stamp impersonation audit fields. story_3548cde2.
	GlobalAdmin bool
}

// UserFrom returns the caller identity attached by AuthMiddleware.
func UserFrom(ctx context.Context) (CallerIdentity, bool) {
	v, ok := ctx.Value(userKey).(CallerIdentity)
	return v, ok
}

// AuthDeps are the satellites dependencies the middleware needs to resolve
// a caller.
type AuthDeps struct {
	Sessions       auth.SessionStore
	Users          auth.UserStoreByID
	APIKeys        []string
	Logger         arbor.ILogger
	OAuthValidator *auth.BearerValidator // optional; when nil OAuth-Bearer path is skipped
}

// AuthMiddleware wraps next with /mcp authentication. Three paths in order
// (story_512cc5cd):
//
//  1. Authorization: Bearer <api-key> matching cfg.APIKeys.
//  2. Authorization: Bearer <token> validated by OAuthValidator (Google /
//     GitHub access tokens, satellites-signed exchange tokens).
//  3. satellites_session cookie.
//
// Unauthenticated requests get 401 + WWW-Authenticate.
func AuthMiddleware(deps AuthDeps) func(http.Handler) http.Handler {
	keyset := make(map[string]struct{}, len(deps.APIKeys))
	for _, k := range deps.APIKeys {
		k = strings.TrimSpace(k)
		if k != "" {
			keyset[k] = struct{}{}
		}
	}
	// story_3548cde2: load the global-admin email set once at middleware
	// construction. Changes require a restart — acceptable while there
	// is a single global admin.
	adminEmails := auth.LoadGlobalAdminEmails()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := bearerToken(r)
			if token != "" {
				// 1. Bearer API key.
				if _, ok := keyset[token]; ok {
					ctx := context.WithValue(r.Context(), userKey, CallerIdentity{
						Email:  "apikey",
						UserID: "apikey",
						Source: "apikey",
					})
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				// 2. OAuth bearer (Google / GitHub / satellites-signed).
				if deps.OAuthValidator != nil {
					if info, err := deps.OAuthValidator.Validate(r.Context(), token); err == nil {
						caller := CallerIdentity{
							Email:  info.Email,
							UserID: info.UserID,
							Source: "oauth:" + info.Provider,
						}
						caller.GlobalAdmin = auth.IsGlobalAdmin(auth.User{Email: info.Email}, adminEmails)
						ctx := context.WithValue(r.Context(), userKey, caller)
						next.ServeHTTP(w, r.WithContext(ctx))
						return
					}
				}
			}
			// 3. Session cookie.
			if id := auth.ReadCookie(r); id != "" {
				sess, err := deps.Sessions.Get(id)
				if err == nil {
					user, err := deps.Users.GetByID(sess.UserID)
					if err == nil {
						caller := CallerIdentity{
							Email:  user.Email,
							UserID: user.ID,
							Source: "session",
						}
						caller.GlobalAdmin = auth.IsGlobalAdmin(user, adminEmails)
						ctx := context.WithValue(r.Context(), userKey, caller)
						next.ServeHTTP(w, r.WithContext(ctx))
						return
					}
				}
			}
			w.Header().Set("WWW-Authenticate", `Bearer realm="satellites"`)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"authentication required"}`))
		})
	}
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}
