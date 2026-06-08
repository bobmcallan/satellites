package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/bobmcallan/satellites/internal/workspace"
)

// Dev-mode credentials. Predictable by construction — anyone reading
// this file knows them. NEVER enable dev mode in a network-reachable
// deployment.
const (
	DevAdminEmail    = "admin@dev.satellites.local"
	DevUserEmail     = "user@dev.satellites.local"
	DevAdminKey      = "sk_dev_admin"
	DevUserKey       = "sk_dev_user"
	DevAdminPassword = "admin"
	DevUserPassword  = "user"
)

// DevSeed creates the admin + user accounts plus their well-known
// api-keys and bcrypt-hashed passwords. Idempotent: a re-run does not
// duplicate rows and re-hashes the same passwords to the same logical
// outcome (bcrypt hashes are non-deterministic but VerifyPassword still
// accepts the raw value).
func (s *Store) DevSeed(ctx context.Context) error {
	admin, err := s.CreateUser(ctx, "usr_dev_admin", DevAdminEmail, "Dev Admin", RoleAdmin)
	if err != nil {
		return fmt.Errorf("devseed: create admin: %w", err)
	}
	if _, _, err := s.IssueAPIKeyWithRaw(ctx, "apk_dev_admin", admin.ID, "", "", DevAdminKey); err != nil {
		return fmt.Errorf("devseed: admin key: %w", err)
	}
	if err := s.SetPassword(ctx, admin.ID, DevAdminPassword); err != nil {
		return fmt.Errorf("devseed: admin password: %w", err)
	}

	user, err := s.CreateUser(ctx, "usr_dev_user", DevUserEmail, "Dev User", RoleUser)
	if err != nil {
		return fmt.Errorf("devseed: create user: %w", err)
	}
	if _, _, err := s.IssueAPIKeyWithRaw(ctx, "apk_dev_user", user.ID, "", "", DevUserKey); err != nil {
		return fmt.Errorf("devseed: user key: %w", err)
	}
	if err := s.SetPassword(ctx, user.ID, DevUserPassword); err != nil {
		return fmt.Errorf("devseed: user password: %w", err)
	}

	// Personal workspace per user (epic:user-admin, sty_3a54bf42) — the
	// shared boot default was retired, so the dev accounts each own one.
	ws := workspace.New(s.DB)
	now := time.Now().UTC()
	if _, err := ws.EnsurePersonalWorkspace(ctx, admin.ID, "Dev Admin", now); err != nil {
		return fmt.Errorf("devseed: admin personal workspace: %w", err)
	}
	if _, err := ws.EnsurePersonalWorkspace(ctx, user.ID, "Dev User", now); err != nil {
		return fmt.Errorf("devseed: user personal workspace: %w", err)
	}

	return nil
}
