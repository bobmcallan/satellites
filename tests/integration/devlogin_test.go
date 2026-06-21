//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/variable"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestDevLoginReconcileAndReveal covers the KV-backed dev-login enabler
// (epic:portal-auth-ux, sty_f228c674): the boot reconcile provisions a
// minimal-role login principal whose credentials live as admin-only secret
// variables, and variable_get reveal returns the plaintext to a global admin
// only — preserving the default redaction for everyone else.
func TestDevLoginReconcileAndReveal(t *testing.T) {
	env := testbootstrap.SetUp(t)

	varStore := variable.New(env.DB)
	authStore := auth.New(env.DB)
	verb.SetVariableStore(varStore)
	verb.SetAuthStore(authStore)
	verb.SetWorkspaceStore(workspace.New(env.DB))
	t.Cleanup(func() {
		verb.SetVariableStore(nil)
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
	})

	if _, err := env.DB.Exec(`TRUNCATE api_keys, users, workspace_members, variables RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if err := authStore.DevSeed(context.Background()); err != nil {
		t.Fatalf("dev seed: %v", err)
	}
	admin, err := authStore.GetUserByEmail(context.Background(), auth.DevAdminEmail)
	if err != nil {
		t.Fatalf("lookup admin: %v", err)
	}
	user, err := authStore.GetUserByEmail(context.Background(), auth.DevUserEmail)
	if err != nil {
		t.Fatalf("lookup user: %v", err)
	}
	ctxAdmin := authWithUser(context.Background(), admin)
	ctxUser := authWithUser(context.Background(), user)

	// First reconcile provisions the dev-login user + creds.
	if err := authStore.ReconcileDevLogin(context.Background(), varStore); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// AC#1/#4: a users row exists at the minimum viewing role (RoleUser).
	devUser, err := authStore.GetUserByEmail(context.Background(), auth.DevLoginEmail)
	if err != nil {
		t.Fatalf("lookup dev-login user: %v", err)
	}
	if devUser.ID != auth.DevLoginUserID {
		t.Fatalf("dev-login id = %q, want %q", devUser.ID, auth.DevLoginUserID)
	}
	if devUser.Role != auth.RoleUser {
		t.Fatalf("dev-login role = %q, want %q (minimum viewer, not admin)", devUser.Role, auth.RoleUser)
	}

	// AC#2: the password lives as a secret variable and VerifyPassword accepts it.
	pwVar, err := varStore.Get(context.Background(), variable.Key{Scope: variable.ScopeSystem, Name: auth.DevLoginPasswordVar})
	if err != nil {
		t.Fatalf("get password var: %v", err)
	}
	if !pwVar.Secret || pwVar.Value == "" {
		t.Fatalf("password var should be a non-empty secret: secret=%v len=%d", pwVar.Secret, len(pwVar.Value))
	}
	if _, err := authStore.VerifyPassword(context.Background(), auth.DevLoginEmail, pwVar.Value); err != nil {
		t.Fatalf("VerifyPassword should accept the KV password: %v", err)
	}

	// AC#2/#5: admin-only reveal.
	t.Run("admin reveal returns the plaintext", func(t *testing.T) {
		raw, err := verb.Dispatch(ctxAdmin, "variable_get", json.RawMessage(
			`{"name":"`+auth.DevLoginPasswordVar+`","scope":"system","reveal":true}`))
		if err != nil {
			t.Fatalf("admin reveal: %v", err)
		}
		var got verb.VariableGetResponse
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got.Value != pwVar.Value || !got.Secret {
			t.Fatalf("admin reveal should return plaintext: value=%q secret=%v", got.Value, got.Secret)
		}
	})

	t.Run("default read still redacts", func(t *testing.T) {
		raw, err := verb.Dispatch(ctxAdmin, "variable_get", json.RawMessage(
			`{"name":"`+auth.DevLoginPasswordVar+`","scope":"system"}`))
		if err != nil {
			t.Fatalf("redacted read: %v", err)
		}
		var got verb.VariableGetResponse
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got.Value != "" || !got.Secret {
			t.Fatalf("default read should redact: value=%q secret=%v", got.Value, got.Secret)
		}
	})

	t.Run("non-admin reveal is forbidden", func(t *testing.T) {
		_, err := verb.Dispatch(ctxUser, "variable_get", json.RawMessage(
			`{"name":"`+auth.DevLoginPasswordVar+`","scope":"system","reveal":true}`))
		if !errors.Is(err, verb.ErrForbidden) {
			t.Fatalf("expected ErrForbidden for non-admin reveal, got %v", err)
		}
	})

	// AC#5 (idempotence): a second reconcile preserves the generated password.
	t.Run("idempotent re-run preserves the password", func(t *testing.T) {
		if err := authStore.ReconcileDevLogin(context.Background(), varStore); err != nil {
			t.Fatalf("second reconcile: %v", err)
		}
		after, err := varStore.Get(context.Background(), variable.Key{Scope: variable.ScopeSystem, Name: auth.DevLoginPasswordVar})
		if err != nil {
			t.Fatalf("get password var after re-run: %v", err)
		}
		if after.Value != pwVar.Value {
			t.Fatalf("re-run changed the password: %q -> %q", pwVar.Value, after.Value)
		}
		if _, err := authStore.VerifyPassword(context.Background(), auth.DevLoginEmail, after.Value); err != nil {
			t.Fatalf("VerifyPassword should still accept after re-run: %v", err)
		}
	})
}
