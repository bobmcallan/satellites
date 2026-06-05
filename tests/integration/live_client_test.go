//go:build integration

package integration_test

import (
	"testing"

	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
	"github.com/chromedp/chromedp"
)

// TestLiveClientLoaded verifies the shared SSE client (sty_b6e39eb8) loads on an
// authed page (via the common _user_menu chrome) and exposes the live.on /
// live.off registration API a page uses to subscribe to trigger topics (AC4).
// End-to-end NOTIFY→stream delivery is covered server-side by
// TestLiveEventsBus; this asserts the browser-side contract is present.
func TestLiveClientLoaded(t *testing.T) {
	env := testbootstrap.SetUpWithServer(t)
	ctx := newBrowserCtx(t)

	var api string
	if err := chromedp.Run(ctx,
		chromedp.Navigate(env.ServerURL+"/login"),
		chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
		chromedp.Click(`button[data-action="dev-login-admin"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
		// live.js loads via the shared chrome; assert its public API exists.
		chromedp.Evaluate(`(typeof window.live) + "/" + (typeof (window.live && window.live.on))`, &api),
	); err != nil {
		t.Fatalf("load authed page: %v", err)
	}
	if api != "object/function" {
		t.Fatalf("window.live API = %q, want \"object/function\" (live.js not loaded or missing on())", api)
	}
}
