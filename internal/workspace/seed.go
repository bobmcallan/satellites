package workspace

import (
	"context"
	"time"

	"github.com/ternarybob/arbor"
)

// DefaultNamePrefix is the display-name prefix used when minting a user's
// default personal workspace. The full name is `Personal (<userID>)` so the
// owner can recognise it in the switcher until they rename.
const DefaultNamePrefix = "Personal"

// EnsureDefault returns the id of the user's default personal workspace,
// creating it if it doesn't already exist. Idempotent: repeat calls return
// the same id.
func EnsureDefault(ctx context.Context, store Store, logger arbor.ILogger, userID string, now time.Time) (string, error) {
	existing, err := store.ListByMember(ctx, userID)
	if err != nil {
		return "", err
	}
	for _, w := range existing {
		if w.OwnerUserID == userID {
			return w.ID, nil
		}
	}
	name := DefaultNamePrefix + " (" + userID + ")"
	w, err := store.Create(ctx, userID, name, now)
	if err != nil {
		return "", err
	}
	logger.Info().
		Str("workspace_id", w.ID).
		Str("user_id", userID).
		Msg("default workspace seeded")
	return w.ID, nil
}
