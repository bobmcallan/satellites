//go:build portalui

package portalui

import (
	"context"
	"strings"
	"testing"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestLanding_NoThemePicker (story_3e7e732a) — the landing page must not
// render the dark/sys/light toggle. Theme handling persists on /login and
// /settings; the landing page itself is toggle-free for v3 visual parity.
func TestLanding_NoThemePicker(t *testing.T) {
	h := StartHarness(t)

	parent, cancel := withTimeout(context.Background(), browserDeadline)
	defer cancel()
	browserCtx, cancelBrowser := newChromedpContext(t, parent)
	defer cancelBrowser()

	var html string
	if err := chromedp.Run(browserCtx,
		network.ClearBrowserCookies(),
		chromedp.Navigate(h.BaseURL+"/"),
		chromedp.WaitVisible(`.landing-wordmark`, chromedp.ByQuery),
		chromedp.OuterHTML("html", &html, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("navigate /: %v", err)
	}
	if strings.Contains(html, "theme-picker") {
		t.Errorf("landing page renders a theme-picker element; expected none for v3 parity")
	}
	if strings.Contains(html, "data-theme-picker") {
		t.Errorf("landing page renders [data-theme-picker]; expected none")
	}

	// Verify theme picker still present on /login (other-pages-unaffected AC).
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(h.BaseURL+"/login"),
		chromedp.WaitVisible(`form.theme-picker`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("/login should still render theme picker: %v", err)
	}
}
