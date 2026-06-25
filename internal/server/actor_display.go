package server

import (
	"context"
	"strings"

	"github.com/bobmcallan/satellites/internal/auth"
)

// newActorResolver returns a memoized resolver that maps a ledger actor's user
// id (e.g. "usr_oauth_google_…") to a human-readable label for the portal
// (sty_4a3a9ebf): the user's EMAIL when known — the operator-facing identity the
// ledger should read as — falling back to the auth.DisplayName chain (profile
// name → email local-part) and finally the raw id, so an unresolved principal
// still renders (never blank, never an error). Lookups are cached per render so
// a ledger of N rows by the same actor costs one user query, not N.
//
// Returned as a plain func so the pure view builders (mergedRows,
// renderLedgerEntries, taskEpisodeViews) take it as an optional argument — nil
// means "no resolution", leaving the raw id, which keeps their unit tests simple.
func newActorResolver(ctx context.Context, cfg Config) func(string) string {
	cache := map[string]string{}
	return func(id string) string {
		id = strings.TrimSpace(id)
		if id == "" {
			return ""
		}
		if v, ok := cache[id]; ok {
			return v
		}
		display := id
		if cfg.Store != nil && cfg.Store.DB != nil {
			if u, err := cfg.Store.GetUserByID(ctx, id); err == nil && u != nil {
				if e := strings.TrimSpace(u.Email); e != "" {
					display = e
				} else {
					display = auth.DisplayName(u, id)
				}
			}
		}
		cache[id] = display
		return display
	}
}

// resolveActor applies an optional resolver, leaving the id untouched when the
// resolver is nil (the unit-test path).
func resolveActor(resolve func(string) string, id string) string {
	if resolve == nil {
		return id
	}
	return resolve(id)
}
