//go:build integration

package integration_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestAuthDevSeed exercises the server-side auth pillar end-to-end:
// dev-mode seed → ValidateKey resolves admin/user → middleware gates
// requests via Authorization: Bearer.
func TestAuthDevSeed(t *testing.T) {
	env := testbootstrap.SetUp(t)
	store := auth.New(env.DB)
	ctx := context.Background()

	t.Run("DevSeed creates admin + user idempotently", func(t *testing.T) {
		testbootstrap.Reset(t, env)
		// Reset only touches the four primitive tables; users + api_keys
		// survive but are also empty. (Reset doesn't know about auth
		// tables; that's by design — clean per-section injection of
		// substrate data, while auth survives so middleware tests work.)
		// Truncate them explicitly here for a known-clean baseline.
		if _, err := env.DB.Exec(`TRUNCATE api_keys, users RESTART IDENTITY CASCADE`); err != nil {
			t.Fatalf("truncate auth: %v", err)
		}

		if err := store.DevSeed(ctx); err != nil {
			t.Fatalf("first seed: %v", err)
		}
		if err := store.DevSeed(ctx); err != nil {
			t.Fatalf("second seed (idempotent): %v", err)
		}

		var nUsers, nKeys int
		if err := env.DB.QueryRow(`SELECT count(*) FROM users`).Scan(&nUsers); err != nil {
			t.Fatal(err)
		}
		if err := env.DB.QueryRow(`SELECT count(*) FROM api_keys`).Scan(&nKeys); err != nil {
			t.Fatal(err)
		}
		if nUsers != 2 {
			t.Errorf("expected 2 users after idempotent seed, got %d", nUsers)
		}
		if nKeys != 2 {
			t.Errorf("expected 2 api_keys after idempotent seed, got %d", nKeys)
		}
	})

	t.Run("ValidateKey resolves dev keys to correct users", func(t *testing.T) {
		// Carries over from previous section — DevSeed is idempotent.
		u, err := store.ValidateKey(ctx, auth.DevAdminKey)
		if err != nil {
			t.Fatalf("validate admin: %v", err)
		}
		if u.Role != auth.RoleAdmin {
			t.Errorf("admin role: got %s want %s", u.Role, auth.RoleAdmin)
		}
		if u.Email != auth.DevAdminEmail {
			t.Errorf("admin email: got %s want %s", u.Email, auth.DevAdminEmail)
		}

		u2, err := store.ValidateKey(ctx, auth.DevUserKey)
		if err != nil {
			t.Fatalf("validate user: %v", err)
		}
		if u2.Role != auth.RoleUser {
			t.Errorf("user role: got %s want %s", u2.Role, auth.RoleUser)
		}
	})

	t.Run("ValidateKey rejects unknown + revoked", func(t *testing.T) {
		if _, err := store.ValidateKey(ctx, "sk_does_not_exist"); !errors.Is(err, auth.ErrInvalidKey) {
			t.Errorf("unknown key: got %v want ErrInvalidKey", err)
		}

		// Issue + revoke.
		raw, k, err := store.IssueAPIKey(ctx, "apk_revoke_test", "usr_dev_admin", "", "tester")
		if err != nil {
			t.Fatalf("issue: %v", err)
		}
		if _, err := store.ValidateKey(ctx, raw); err != nil {
			t.Fatalf("validate before revoke: %v", err)
		}
		if err := store.RevokeKey(ctx, k.ID); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		if _, err := store.ValidateKey(ctx, raw); !errors.Is(err, auth.ErrInvalidKey) {
			t.Errorf("after revoke: got %v want ErrInvalidKey", err)
		}
	})

	t.Run("middleware gates requests via Authorization: Bearer", func(t *testing.T) {
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if u := auth.FromContext(r.Context()); u != nil {
				w.Header().Set("X-User-Email", u.Email)
			}
			w.WriteHeader(http.StatusNoContent)
		})
		handler := store.Middleware(inner)

		// No header → 401, inner not called.
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("POST", "/mcp", nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("no header: got %d want 401", rec.Code)
		}

		// Bad key → 401.
		rec = httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/mcp", nil)
		req.Header.Set("Authorization", "Bearer sk_garbage")
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("bad key: got %d want 401", rec.Code)
		}

		// Valid admin key → 204 + email header attached by middleware.
		rec = httptest.NewRecorder()
		req = httptest.NewRequest("POST", "/mcp", nil)
		req.Header.Set("Authorization", "Bearer "+auth.DevAdminKey)
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Errorf("good key: got %d want 204; body=%s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("X-User-Email"); got != auth.DevAdminEmail {
			t.Errorf("user email header: got %s want %s", got, auth.DevAdminEmail)
		}

		// /oauth/ paths bypass auth → inner runs with no user attached.
		rec = httptest.NewRecorder()
		req = httptest.NewRequest("GET", "/oauth/github/login", nil)
		handler.ServeHTTP(rec, req)
		if rec.Code == http.StatusUnauthorized {
			t.Errorf("oauth path: got 401, expected bypass to invoke inner handler")
		}
		if got := rec.Header().Get("X-User-Email"); got != "" {
			t.Errorf("oauth path: inner saw a user (X-User-Email=%s) but auth was bypassed", got)
		}
		_ = strings.Contains // keep imports stable
	})
}
