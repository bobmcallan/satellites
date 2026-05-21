package auth

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMiddleware_Unauthenticated_SetsWWWAuthenticate(t *testing.T) {
	// Store with a nil DB is fine here — the request is rejected before
	// ValidateKey is reached.
	s := &Store{DB: (*sql.DB)(nil)}

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	h := s.Middleware(next)

	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Host = "satellites-pprod.fly.dev"
	r.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if called {
		t.Fatal("next handler was invoked; auth gate failed")
	}
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	wantHeader := `Bearer resource_metadata="https://satellites-pprod.fly.dev/.well-known/oauth-protected-resource"`
	if got := w.Header().Get("WWW-Authenticate"); got != wantHeader {
		t.Errorf("WWW-Authenticate = %q, want %q", got, wantHeader)
	}
	if body := strings.TrimSpace(w.Body.String()); body != "missing bearer credential" {
		t.Errorf("body = %q, want %q", body, "missing bearer credential")
	}
}

func TestMiddleware_SkipsOAuthPath(t *testing.T) {
	s := &Store{DB: (*sql.DB)(nil)}

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	h := s.Middleware(next)

	r := httptest.NewRequest("GET", "/oauth/google/callback", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if !called {
		t.Fatal("/oauth/* path was gated; expected pass-through")
	}
	if got := w.Header().Get("WWW-Authenticate"); got != "" {
		t.Errorf("WWW-Authenticate set on /oauth/* path: %q", got)
	}
}
