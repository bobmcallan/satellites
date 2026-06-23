//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/bobmcallan/satellites/internal/auth"
)

// authWithUser stamps an auth.User onto ctx the same way
// auth.Middleware does in the live HTTP path. Tests live in a
// different package than internal/auth, so they go through the
// exported WithUser helper rather than the unexported ctxKey.
func authWithUser(ctx context.Context, u *auth.User) context.Context {
	return auth.WithUser(ctx, u)
}

// withSkillReview injects a valid review attestation into a behaviour-kind
// (skill / principle-tagged) document_upsert request map — mirroring what
// `satellites <kind> upload` mints after the per-type reviewer accepts. The
// verb barrier (sty_e6226180, verifyReviewAttestation) requires decision=accept
// and content_sha256==sha256(body); raw-verb integration upserts must carry it
// or the upsert is forbidden before reaching the behaviour under test
// (sty_50ae9b96). Returns the same map for inline use.
func withSkillReview(req map[string]any) map[string]any {
	body, _ := req["body"].(string)
	sum := sha256.Sum256([]byte(body))
	req["review"] = map[string]any{
		"decision":       "accept",
		"content_sha256": hex.EncodeToString(sum[:]),
	}
	return req
}
