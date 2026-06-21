//go:build integration

package integration_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/live"
	"github.com/bobmcallan/satellites/internal/server"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestSessionExpiry covers the session-expiry redirect/restore story
// (epic:portal-auth-ux, sty_6a1fe460): programmatic endpoints return 401 (so the
// client can detect logout), and password /login honours a same-site `next`
// return target while rejecting a hostile one.
func TestSessionExpiry(t *testing.T) {
	env := testbootstrap.SetUp(t)

	authStore := auth.New(env.DB)
	if err := authStore.DevSeed(context.Background()); err != nil {
		t.Fatalf("dev seed: %v", err)
	}
	sessions := auth.NewSessions([]byte("session-expiry-test-secret"))
	// Wire Live + LiveScope so the /events route is registered (the handler's
	// session check 401s before the scope is ever resolved).
	handler := server.Build(server.Config{
		Store:    authStore,
		Sessions: sessions,
		DevMode:  true,
		Live:     live.NewHub(),
		LiveScope: func(context.Context, string) (live.Scope, error) {
			return live.Scope{}, nil
		},
	})

	// AC#2: unauthenticated programmatic requests return 401, not a 303 to login.
	t.Run("unauthenticated programmatic requests return 401", func(t *testing.T) {
		for _, path := range []string{
			"/events",
			"/stories/sty_anything/trace.fragment",
			"/projects/proj_anything/stories.fragment",
		} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("GET %s (no session): status %d, want 401", path, rec.Code)
			}
		}
	})

	// Control: a full-page GET keeps its 303 → /login.
	t.Run("unauthenticated full-page GET still 303s to /login", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/projects", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
			t.Fatalf("GET /projects (no session): status %d loc %q, want 303 /login", rec.Code, rec.Header().Get("Location"))
		}
	})

	postLogin := func(t *testing.T, next string) *httptest.ResponseRecorder {
		t.Helper()
		form := url.Values{
			"email":    {auth.DevAdminEmail},
			"password": {auth.DevAdminPassword},
			"next":     {next},
		}
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	// AC#3: a valid same-site next round-trips back to that exact view.
	t.Run("login with a valid next returns to it", func(t *testing.T) {
		const next = "/projects/proj_fc7d72d8?status=open&order=priority"
		rec := postLogin(t, next)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("POST /login: status %d, want 303", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != next {
			t.Fatalf("post-login redirect = %q, want %q", loc, next)
		}
	})

	// AC#4: a hostile next is rejected to "/".
	t.Run("login with a hostile next falls back to /", func(t *testing.T) {
		rec := postLogin(t, "//evil.com/phish")
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("POST /login: status %d, want 303", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/" {
			t.Fatalf("hostile next should fall back to /, got %q", loc)
		}
	})
}
