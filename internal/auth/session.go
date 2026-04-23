package auth

import (
	"net/http"
	"time"
)

// CookieName is the session cookie label. Exported so the integration test
// can assert against it.
const CookieName = "satellites_session"

// DefaultSessionTTL is the session expiry applied at login time.
const DefaultSessionTTL = 24 * time.Hour

// CookieOptions are the per-environment cookie flags applied by WriteCookie
// and ClearCookie. Secure is true only under prod to avoid breaking dev
// flows on plain HTTP localhost.
type CookieOptions struct {
	Secure bool
}

// WriteCookie sets the session cookie on w. Lifetime tracks the session's
// ExpiresAt; SameSite is Lax so top-nav OAuth redirects don't strip it;
// HttpOnly so JS can't read it.
func WriteCookie(w http.ResponseWriter, sess Session, opts CookieOptions) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    sess.ID,
		Path:     "/",
		HttpOnly: true,
		Secure:   opts.Secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  sess.ExpiresAt,
	})
}

// ClearCookie writes a zero-value cookie with MaxAge -1 so browsers drop it.
func ClearCookie(w http.ResponseWriter, opts CookieOptions) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   opts.Secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// ReadCookie returns the session id from the request, or "" when absent.
func ReadCookie(r *http.Request) string {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return ""
	}
	return c.Value
}
