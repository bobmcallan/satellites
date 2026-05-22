//go:build integration

package integration_test

import (
	"context"

	"github.com/bobmcallan/satellites/internal/auth"
)

// authWithUser stamps an auth.User onto ctx the same way
// auth.Middleware does in the live HTTP path. Tests live in a
// different package than internal/auth, so they go through the
// exported WithUser helper rather than the unexported ctxKey.
func authWithUser(ctx context.Context, u *auth.User) context.Context {
	return auth.WithUser(ctx, u)
}
