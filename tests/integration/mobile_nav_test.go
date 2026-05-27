//go:build integration

package integration_test

import (
	"testing"

	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
)

// TestMobileNav exercises sty_63116a75 — at narrow viewports the
// inline primary-nav collapses and re-appears at the top of the user
// menu dropdown. Pure-CSS switch (@media queries); the test just
// emulates two viewport sizes and asserts visibility on both sides.
func TestMobileNav(t *testing.T) {
	env := testbootstrap.SetUpWithServer(t)
	ctx := newBrowserCtx(t)

	// Login.
	if err := chromedp.Run(ctx,
		chromedp.Navigate(env.ServerURL+"/login"),
		chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
		chromedp.Click(`button[data-action="dev-login-admin"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("login: %v", err)
	}

	setViewport := func(t *testing.T, width, height int64) {
		t.Helper()
		if err := chromedp.Run(ctx,
			emulation.SetDeviceMetricsOverride(width, height, 1, false),
		); err != nil {
			t.Fatalf("viewport %dx%d: %v", width, height, err)
		}
	}

	visibility := func(t *testing.T) map[string]bool {
		t.Helper()
		var got map[string]bool
		// Compute visibility of each candidate selector via offsetParent.
		js := `(() => {
			const sel = {
				primaryNavProjects: 'a.nav-item[data-nav="projects"]',
				avatar: '[data-field="user-menu-avatar"]',
				mobileNav: '[data-field="mobile-nav"]'
			};
			const out = {};
			for (const k in sel) {
				const el = document.querySelector(sel[k]);
				out[k] = !!(el && el.offsetParent !== null);
			}
			return out;
		})()`
		if err := chromedp.Run(ctx, chromedp.Evaluate(js, &got)); err != nil {
			t.Fatalf("evaluate visibility: %v", err)
		}
		return got
	}

	t.Run("desktop viewport: inline nav visible, mobile-nav hidden", func(t *testing.T) {
		setViewport(t, 1280, 720)
		// Navigate fresh so CSS re-applies cleanly.
		if err := chromedp.Run(ctx,
			chromedp.Navigate(env.ServerURL+"/"),
			chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("nav: %v", err)
		}
		// Open the dropdown so mobile-nav (if rendered) becomes
		// candidate-visible — its parent must be open.
		if err := chromedp.Run(ctx,
			chromedp.Click(`[data-action="user-menu-toggle"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-field="user-menu-panel"]`, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("open menu: %v", err)
		}
		vis := visibility(t)
		if !vis["primaryNavProjects"] {
			t.Error("desktop: inline PROJECTS nav-item should be visible")
		}
		if !vis["avatar"] {
			t.Error("desktop: avatar should be visible")
		}
		if vis["mobileNav"] {
			t.Error("desktop: mobile-nav section in dropdown should be hidden")
		}
	})

	t.Run("mobile viewport: inline nav hidden, mobile-nav visible in panel", func(t *testing.T) {
		setViewport(t, 600, 800)
		if err := chromedp.Run(ctx,
			chromedp.Navigate(env.ServerURL+"/"),
			chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("nav: %v", err)
		}
		// Open the dropdown.
		if err := chromedp.Run(ctx,
			chromedp.Click(`[data-action="user-menu-toggle"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-field="user-menu-panel"]`, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("open menu: %v", err)
		}
		vis := visibility(t)
		if vis["primaryNavProjects"] {
			t.Error("mobile: inline PROJECTS nav-item should be hidden")
		}
		if !vis["avatar"] {
			t.Error("mobile: avatar should still be visible")
		}
		if !vis["mobileNav"] {
			t.Error("mobile: mobile-nav section should be visible in open dropdown")
		}
	})
}
