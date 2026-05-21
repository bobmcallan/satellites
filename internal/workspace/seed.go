package workspace

import (
	"context"
	"errors"
	"time"
)

// DefaultWorkspaceName is the name the boot-time seed assigns to the
// auto-created workspace. Chosen to match v4 so operator muscle memory
// carries over.
const DefaultWorkspaceName = "default"

// SeedDefault ensures one workspace flagged is_default exists. Returns
// the existing default workspace when one is already present;
// otherwise mints a NULL-owner workspace named "default" and flags it.
// Idempotent — safe to call on every boot.
func SeedDefault(ctx context.Context, s *Store, now time.Time) (Workspace, error) {
	w, err := s.GetDefault(ctx)
	if err == nil {
		return w, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Workspace{}, err
	}
	w, err = s.Create(ctx, "", DefaultWorkspaceName, now)
	if err != nil {
		return Workspace{}, err
	}
	return s.SetDefault(ctx, w.ID, now)
}
