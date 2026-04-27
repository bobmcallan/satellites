//go:build portalui

package portalui

import (
	"context"
	"strings"
	"testing"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestLanding_NoAuthBanner_VisibleWhenNoProviders (story_fd2cc52f AC2)
// — when no OAuth credentials are configured AND DevMode is disabled,
// the landing page must surface a visible diagnostic banner so the
// failure mode is legible to the operator. Without this banner the
// landing page silently elides every auth surface except the
// username/password form, which gives no signal that an admin needs to
// configure GOOGLE_CLIENT_ID / GITHUB_CLIENT_ID / DEV_MODE.
func TestLanding_NoAuthBanner_VisibleWhenNoProviders(t *testing.T) {
	h := StartHarness(t)

	// Strip every auto-rendered sign-in path so the diagnostic block is
	// the only AuthBanner-relevant render. The harness defaults to
	// DevMode=true / Env=dev; flip both off and leave the OAuth client
	// IDs unset (the harness never sets them).
	h.AuthHandlers.Cfg.DevMode = false
	h.AuthHandlers.Cfg.Env = "prod"

	parent, cancel := withTimeout(context.Background(), browserDeadline)
	defer cancel()
	browserCtx, cancelBrowser := newChromedpContext(t, parent)
	defer cancelBrowser()

	var bannerText string
	if err := chromedp.Run(browserCtx,
		network.ClearBrowserCookies(),
		chromedp.Navigate(h.BaseURL+"/"),
		chromedp.WaitVisible(`aside[data-testid="no-auth-banner"]`, chromedp.ByQuery),
		chromedp.Text(`aside[data-testid="no-auth-banner"]`, &bannerText, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("navigate /: %v", err)
	}

	for _, want := range []string{
		"No sign-in providers configured",
		"GOOGLE_CLIENT_ID",
		"GITHUB_CLIENT_ID",
		"DEV_MODE",
	} {
		if !strings.Contains(bannerText, want) {
			t.Errorf("no-auth banner missing %q\nbanner=%s", want, bannerText)
		}
	}
}

// TestLanding_NoAuthBanner_HiddenWhenDevMode (story_fd2cc52f AC2
// inverse) — when DevMode is enabled, the diagnostic banner must NOT
// render. The DevMode quick-signin path is the operator-visible signal
// that auth is configured for local use; surfacing the banner alongside
// it would be noise.
func TestLanding_NoAuthBanner_HiddenWhenDevMode(t *testing.T) {
	h := StartHarness(t)

	parent, cancel := withTimeout(context.Background(), browserDeadline)
	defer cancel()
	browserCtx, cancelBrowser := newChromedpContext(t, parent)
	defer cancelBrowser()

	var hits int
	if err := chromedp.Run(browserCtx,
		network.ClearBrowserCookies(),
		chromedp.Navigate(h.BaseURL+"/"),
		chromedp.WaitVisible(`form[data-testid="dev-signin"] button`, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelectorAll('aside[data-testid="no-auth-banner"]').length`, &hits),
	); err != nil {
		t.Fatalf("navigate /: %v", err)
	}
	if hits != 0 {
		t.Errorf("no-auth banner rendered alongside DevMode path; hits=%d", hits)
	}
}
