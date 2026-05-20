package auth

import (
	"context"
	"fmt"
)

// Dev-mode credentials. Predictable by construction — anyone reading
// this file knows them. NEVER enable dev mode in a network-reachable
// deployment.
const (
	DevAdminEmail = "admin@dev.satellites.local"
	DevUserEmail  = "user@dev.satellites.local"
	DevAdminKey   = "sk_dev_admin"
	DevUserKey    = "sk_dev_user"
)

// DevSeed creates the admin + user accounts plus their well-known
// api-keys. Idempotent: a re-run does not duplicate rows.
func (s *Store) DevSeed(ctx context.Context) error {
	admin, err := s.CreateUser(ctx, "usr_dev_admin", DevAdminEmail, "Dev Admin", RoleAdmin)
	if err != nil {
		return fmt.Errorf("devseed: create admin: %w", err)
	}
	if _, _, err := s.IssueAPIKeyWithRaw(ctx, "apk_dev_admin", admin.ID, "", "", DevAdminKey); err != nil {
		return fmt.Errorf("devseed: admin key: %w", err)
	}

	user, err := s.CreateUser(ctx, "usr_dev_user", DevUserEmail, "Dev User", RoleUser)
	if err != nil {
		return fmt.Errorf("devseed: create user: %w", err)
	}
	if _, _, err := s.IssueAPIKeyWithRaw(ctx, "apk_dev_user", user.ID, "", "", DevUserKey); err != nil {
		return fmt.Errorf("devseed: user key: %w", err)
	}

	return nil
}
