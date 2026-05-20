//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestSatellitesInit_WithAuthStore exercises the verb end-to-end against
// a live Postgres + seeded auth.Store + ctx-attached user.
func TestSatellitesInit_WithAuthStore(t *testing.T) {
	env := testbootstrap.SetUp(t)
	store := auth.New(env.DB)

	// Truncate auth tables and seed dev users.
	if _, err := env.DB.Exec(`TRUNCATE api_keys, users RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate auth: %v", err)
	}
	if err := store.DevSeed(context.Background()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Wire the store into the verb package (mirrors satellites-server boot).
	verb.SetAuthStore(store)
	t.Cleanup(func() { verb.SetAuthStore(nil) })

	t.Run("unauthenticated context returns auth_login", func(t *testing.T) {
		resp, err := verb.Dispatch(context.Background(), "satellites_init", json.RawMessage(`{"os":"linux","arch":"amd64"}`))
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		var got verb.SatellitesInitResponse
		if err := json.Unmarshal(resp, &got); err != nil {
			t.Fatal(err)
		}
		if got.AuthBootstrap == nil || got.AuthBootstrap.Kind != "auth_login" {
			t.Errorf("expected auth_login (no ctx user), got %+v", got.AuthBootstrap)
		}
	})

	t.Run("authenticated context mints api-key", func(t *testing.T) {
		// Build a ctx with the admin user, as if the middleware had run.
		admin, err := store.GetUserByEmail(context.Background(), auth.DevAdminEmail)
		if err != nil {
			t.Fatalf("lookup admin: %v", err)
		}
		ctx := authWithUser(context.Background(), admin)

		req := `{
            "current_version": "v0.0.1",
            "project_id": "proj_test_init",
            "agent_name": "test_agent",
            "os": "linux",
            "arch": "amd64"
        }`
		raw, err := verb.Dispatch(ctx, "satellites_init", json.RawMessage(req))
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}

		var got verb.SatellitesInitResponse
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}

		// State: current_version != server version (dev) → update_available
		if got.State != "update_available" {
			t.Errorf("state: got %q want update_available", got.State)
		}

		// AuthBootstrap is populated and minted a real key.
		if got.AuthBootstrap == nil || got.AuthBootstrap.Kind != "ready" {
			t.Fatalf("auth_bootstrap kind: got %+v want ready", got.AuthBootstrap)
		}
		if !strings.HasPrefix(got.AuthBootstrap.APIKey, "sk_") {
			t.Errorf("api_key prefix: got %s", got.AuthBootstrap.APIKey)
		}
		if got.AuthBootstrap.APIKeyID == "" {
			t.Errorf("api_key_id empty")
		}

		// Validate the minted key actually authenticates.
		u, err := store.ValidateKey(ctx, got.AuthBootstrap.APIKey)
		if err != nil {
			t.Errorf("minted key did not validate: %v", err)
		}
		if u != nil && u.Email != auth.DevAdminEmail {
			t.Errorf("validated user email: got %s want %s", u.Email, auth.DevAdminEmail)
		}

		// Install info has correct shape.
		if got.Install == nil {
			t.Fatal("install missing")
		}
		if !strings.Contains(got.Install.DownloadURL, "linux-amd64") {
			t.Errorf("download URL platform: got %s", got.Install.DownloadURL)
		}
		if !strings.HasSuffix(got.Install.SHA256URL, ".sha256") {
			t.Errorf("sha256 url shape: got %s", got.Install.SHA256URL)
		}
		if got.TargetInstallPath != "./.satellites/satellites" {
			t.Errorf("target install path: got %s", got.TargetInstallPath)
		}
		if got.TargetConfigPath != "./.satellites/config.json" {
			t.Errorf("target config path: got %s", got.TargetConfigPath)
		}
	})
}

// authWithUser builds a context with the user attached the same way
// auth.Middleware would. Tests live in a different package from
// internal/auth so we can't reach the unexported ctxKey directly —
// route through the exported FromContext + a tiny in-package helper.
// We rely on auth.FromContext to pick up the value via auth.Middleware
// in production; for tests, use a small helper exported via test.
func authWithUser(ctx context.Context, u *auth.User) context.Context {
	return auth.WithUser(ctx, u)
}
