package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// SessionCookieName is the cookie the browser stores after login.
const SessionCookieName = "satellites_session"

// SessionTTL is how long a login is valid. Short enough for dev work
// but long enough that operators don't constantly re-auth.
const SessionTTL = 24 * time.Hour

// ErrInvalidSession is returned when a cookie value is missing, malformed,
// expired, or fails HMAC verification.
var ErrInvalidSession = errors.New("auth: invalid session")

// Sessions issues + verifies HMAC-signed session cookies. Cookie value is
// "<user_id>|<expiry_unix>|<base64-hmac-sha256(user_id|expiry, secret)>"
// — no DB roundtrip per request; revocation is via short TTL.
type Sessions struct {
	Secret []byte
}

// NewSessions returns a Sessions configured with the given secret. The
// caller is responsible for keeping the secret stable across server
// restarts in prod (env var, secrets manager, …); ephemeral randomness
// is fine for tests.
func NewSessions(secret []byte) *Sessions {
	if len(secret) == 0 {
		// Fallback: random per-process secret. Cookies don't survive a
		// restart, but that's a feature in dev.
		secret = make([]byte, 32)
		_, _ = rand.Read(secret)
	}
	return &Sessions{Secret: secret}
}

// Issue writes a fresh session cookie for the given user onto w.
func (s *Sessions) Issue(w http.ResponseWriter, userID string) {
	expiry := time.Now().Add(SessionTTL).Unix()
	value := s.sign(userID, expiry)
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(SessionTTL.Seconds()),
	})
}

// Clear writes an expired cookie to log the user out.
func (s *Sessions) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// UserID returns the verified user id encoded in the request's session
// cookie. Returns ErrInvalidSession when missing/malformed/expired.
func (s *Sessions) UserID(r *http.Request) (string, error) {
	c, err := r.Cookie(SessionCookieName)
	if err != nil {
		return "", ErrInvalidSession
	}
	parts := strings.Split(c.Value, "|")
	if len(parts) != 3 {
		return "", ErrInvalidSession
	}
	uid, expStr, mac := parts[0], parts[1], parts[2]
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return "", ErrInvalidSession
	}
	if time.Now().Unix() >= exp {
		return "", ErrInvalidSession
	}
	if !hmac.Equal([]byte(mac), []byte(s.computeMAC(uid, exp))) {
		return "", ErrInvalidSession
	}
	return uid, nil
}

func (s *Sessions) sign(userID string, expiry int64) string {
	return fmt.Sprintf("%s|%d|%s", userID, expiry, s.computeMAC(userID, expiry))
}

func (s *Sessions) computeMAC(userID string, expiry int64) string {
	h := hmac.New(sha256.New, s.Secret)
	fmt.Fprintf(h, "%s|%d", userID, expiry)
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// SecretFromHex parses a hex-encoded secret string (e.g. from env var).
// Empty input returns nil so NewSessions falls back to random.
func SecretFromHex(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	return hex.DecodeString(s)
}
