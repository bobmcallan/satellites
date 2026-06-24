//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/chromedp/chromedp"
)

// chromedpHeadlessOpts returns the shared headless-Chrome allocator options
// used by every browser test in this package. (Moved here from
// project_detail_chromedp_test.go when sty_af7d7b1a removed the project-detail
// session-race quarantines — the helper outlives those tests.)
func chromedpHeadlessOpts() []chromedp.ExecAllocatorOption {
	return append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		// CI runners have a tiny /dev/shm; without this Chrome's DevTools
		// websocket hangs ("websocket url timeout reached") and chromedp tests
		// fail. Route shared memory to /tmp instead (sty_ae474ca3).
		chromedp.Flag("disable-dev-shm-usage", true),
	)
}

// skipInCI quarantines a test on a GH runner (CI=true) with a NAMED reason — never
// a blanket disable: the test still runs locally. sty_353310ce landed the CI
// integration gate over the tests that pass on a runner. The project-detail/story
// session-race quarantines were retired by sty_af7d7b1a (restructured to
// browser-free server-render tests + static-page client-JS tests in
// internal/server); the remaining skips are browser-needs (codegraph render) and
// claude-dependent upload/review, un-quarantined by sty_52aadf5e once headless
// Chrome / the reviewer are available on the runner.
func skipInCI(t *testing.T, reason string) {
	t.Helper()
	if os.Getenv("CI") != "" {
		t.Skip("skipInCI (sty_52aadf5e): " + reason)
	}
}

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
