package auth

import (
	"context"
	"net/http"
	"net/url"
)

type ctxKey int

const userKey ctxKey = iota

// UserFrom returns the authenticated user on ctx, or zero + false when no
// session resolved.
func UserFrom(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(userKey).(User)
	return u, ok
}

// RequireSession is middleware that resolves the session cookie to a user
// and attaches it to the request context. Unauthenticated requests are
// redirected to /login with the original URL preserved in `?next=`.
func RequireSession(sessions SessionStore, users UserStoreByID, opts CookieOptions) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := ReadCookie(r)
			if id == "" {
				redirectToLogin(w, r)
				return
			}
			sess, err := sessions.Get(id)
			if err != nil {
				ClearCookie(w, opts)
				redirectToLogin(w, r)
				return
			}
			user, err := users.GetByID(sess.UserID)
			if err != nil {
				_ = sessions.Delete(sess.ID)
				ClearCookie(w, opts)
				redirectToLogin(w, r)
				return
			}
			ctx := context.WithValue(r.Context(), userKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// UserStoreByID is the lookup surface RequireSession needs. The primary
// MemoryUserStore keys by email; we add an id-lookup helper there.
type UserStoreByID interface {
	GetByID(id string) (User, error)
}

func redirectToLogin(w http.ResponseWriter, r *http.Request) {
	next := r.URL.Path
	if r.URL.RawQuery != "" {
		next += "?" + r.URL.RawQuery
	}
	loc := "/login?next=" + url.QueryEscape(next)
	http.Redirect(w, r, loc, http.StatusSeeOther)
}
