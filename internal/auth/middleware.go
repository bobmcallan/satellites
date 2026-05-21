package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/bobmcallan/satellites/internal/arbor"
)

type ctxKey struct{}

// FromContext returns the authenticated user attached to the request
// context, or nil if unauthenticated.
func FromContext(ctx context.Context) *User {
	u, _ := ctx.Value(ctxKey{}).(*User)
	return u
}

// WithUser attaches a user to ctx the same way Middleware does.
// Exported for tests and for in-process callers that have already
// authenticated through a non-HTTP channel (e.g. CLI dispatching
// server-side verbs in-process during dev).
func WithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

// Middleware authenticates incoming HTTP requests against the api-keys
// table. On success, attaches the user to the request context. On
// failure, returns 401.
//
// Paths under /oauth/ are skipped — they implement their own auth flow.
func (s *Store) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/oauth/") {
			next.ServeHTTP(w, r)
			return
		}

		h := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(h, prefix) {
			setWWWAuthenticate(w, r)
			http.Error(w, "missing bearer credential", http.StatusUnauthorized)
			return
		}
		key := strings.TrimPrefix(h, prefix)

		u, err := s.ValidateKey(r.Context(), key)
		if err != nil {
			if errors.Is(err, ErrInvalidKey) {
				setWWWAuthenticate(w, r)
				http.Error(w, "invalid credential", http.StatusUnauthorized)
				return
			}
			arbor.ErrorCtx(r.Context(), "auth: validate key", "err", err)
			http.Error(w, "auth error", http.StatusInternalServerError)
			return
		}

		r = r.WithContext(WithUser(r.Context(), u))
		next.ServeHTTP(w, r)
	})
}

// setWWWAuthenticate points unauthenticated /mcp callers at the
// protected-resource metadata so MCP-SDK clients can discover the
// authorization server (RFC 9728).
func setWWWAuthenticate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate", fmt.Sprintf(
		`Bearer resource_metadata="%s/.well-known/oauth-protected-resource"`,
		SchemeAndHost(r),
	))
}
