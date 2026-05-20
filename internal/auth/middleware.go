package auth

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
)

type ctxKey struct{}

// FromContext returns the authenticated user attached to the request
// context, or nil if unauthenticated.
func FromContext(ctx context.Context) *User {
	u, _ := ctx.Value(ctxKey{}).(*User)
	return u
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
			http.Error(w, "missing bearer credential", http.StatusUnauthorized)
			return
		}
		key := strings.TrimPrefix(h, prefix)

		u, err := s.ValidateKey(r.Context(), key)
		if err != nil {
			if errors.Is(err, ErrInvalidKey) {
				http.Error(w, "invalid credential", http.StatusUnauthorized)
				return
			}
			log.Printf("auth: validate key: %v", err)
			http.Error(w, "auth error", http.StatusInternalServerError)
			return
		}

		r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, u))
		next.ServeHTTP(w, r)
	})
}
