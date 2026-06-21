package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/bobmcallan/satellites/internal/variable"
)

// Dev-login (KV-backed) constants. Unlike the DevSeed accounts, this
// principal is provisioned in EVERY deployment so an agent can authenticate
// to the live portal headlessly through the same /login password door a human
// uses (epic:portal-auth-ux). Its password is NOT predictable: it is generated
// randomly on first boot and stored as an admin-only-readable secret variable.
const (
	// DevLoginUserID is the stable id for the reconciled dev-login user row.
	DevLoginUserID = "usr_devlogin"
	// DevLoginEmail is the dedicated, non-human email seeded into
	// dev_login.email when the operator has not set their own. A distinct
	// email keeps the reconcile from ever touching a real account's password.
	DevLoginEmail = "agent@dogfood.satellites.local"
	// DevLoginEmailVar / DevLoginPasswordVar are the system-scope secret
	// variable names the creds live under (see reservedSecretNames in
	// internal/verb/variable.go, which forces both to be written as secrets).
	DevLoginEmailVar    = "dev_login.email"
	DevLoginPasswordVar = "dev_login.password"
)

// ReconcileDevLogin provisions the KV-backed dev-login principal idempotently.
//
//   - Ensures dev_login.email + dev_login.password exist as secret system
//     variables — seeding the dedicated email and a freshly generated random
//     password when absent, and otherwise leaving an operator-set value (e.g. a
//     rotated password) untouched.
//   - Ensures a users row (RoleUser — the minimum a viewer needs) keyed on that
//     email exists, and syncs its bcrypt password_hash from the KV value so
//     VerifyPassword accepts the stored credential.
//
// It reads the secret values directly via the variable store, the same
// server-internal path the LLM-config resolver uses for gemini.api_key — the
// redacting verb is not involved. Safe to run on every boot.
func (s *Store) ReconcileDevLogin(ctx context.Context, varStore *variable.Store) error {
	if varStore == nil {
		return fmt.Errorf("auth: reconcile dev-login: nil variable store")
	}
	now := time.Now().UTC()

	email, err := ensureSecretSystemVar(ctx, varStore, DevLoginEmailVar, func() (string, error) {
		return DevLoginEmail, nil
	}, now)
	if err != nil {
		return fmt.Errorf("auth: reconcile dev-login email: %w", err)
	}
	password, err := ensureSecretSystemVar(ctx, varStore, DevLoginPasswordVar, randomPassword, now)
	if err != nil {
		return fmt.Errorf("auth: reconcile dev-login password: %w", err)
	}

	user, err := s.CreateUser(ctx, DevLoginUserID, email, "Agent (dogfood)", RoleUser)
	if err != nil {
		return fmt.Errorf("auth: reconcile dev-login user: %w", err)
	}
	if err := s.SetPassword(ctx, user.ID, password); err != nil {
		return fmt.Errorf("auth: reconcile dev-login password hash: %w", err)
	}
	return nil
}

// ensureSecretSystemVar returns the existing value of a secret system variable,
// or seeds it (secret=true) from gen() when absent and returns the seeded
// value. An existing value is never overwritten.
func ensureSecretSystemVar(ctx context.Context, varStore *variable.Store, name string, gen func() (string, error), now time.Time) (string, error) {
	key := variable.Key{Scope: variable.ScopeSystem, Name: name}
	v, err := varStore.Get(ctx, key)
	switch {
	case err == nil:
		return v.Value, nil
	case errors.Is(err, variable.ErrNotFound):
		val, genErr := gen()
		if genErr != nil {
			return "", genErr
		}
		if _, err := varStore.Set(ctx, variable.SetInput{Key: key, Value: val, Secret: true}, now); err != nil {
			return "", err
		}
		return val, nil
	default:
		return "", err
	}
}

// randomPassword returns a high-entropy random password (32 hex chars).
func randomPassword() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: rng: %w", err)
	}
	return hex.EncodeToString(b), nil
}
