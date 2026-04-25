//go:build portalui

package portalui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestThemePicker_DefaultDark verifies AC1: a fresh visit (no cookies)
// resolves to data-theme="dark" via the inline first-paint script.
func TestThemePicker_DefaultDark(t *testing.T) {
	h := StartHarness(t)

	parent, cancel := withTimeout(context.Background(), browserDeadline)
	defer cancel()
	browserCtx, cancelBrowser := newChromedpContext(t, parent)
	defer cancelBrowser()

	var theme string
	if err := chromedp.Run(browserCtx,
		network.ClearBrowserCookies(),
		chromedp.Navigate(h.BaseURL+"/login"),
		chromedp.WaitVisible(`form.theme-picker`, chromedp.ByQuery),
		chromedp.AttributeValue("html", "data-theme", &theme, nil),
	); err != nil {
		t.Fatalf("navigate /login: %v", err)
	}
	if theme != "dark" {
		t.Errorf("data-theme = %q, want \"dark\" (AC1)", theme)
	}
}

// TestThemePicker_PersistsAndFlips verifies AC2: clicking each picker
// writes the satellites_theme cookie and the data-theme attribute reflects
// the choice; reload preserves the selection.
func TestThemePicker_PersistsAndFlips(t *testing.T) {
	h := StartHarness(t)

	parent, cancel := withTimeout(context.Background(), browserDeadline)
	defer cancel()
	browserCtx, cancelBrowser := newChromedpContext(t, parent)
	defer cancelBrowser()

	cases := []struct {
		mode      string
		wantTheme string
	}{
		{"light", "light"},
		{"dark", "dark"},
		// system resolves via prefers-color-scheme — skip exact theme assertion
		// here; covered by TestThemePicker_SystemMode.
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			var theme string
			if err := chromedp.Run(browserCtx,
				network.ClearBrowserCookies(),
				chromedp.Navigate(h.BaseURL+"/login"),
				chromedp.WaitVisible(`form.theme-picker button[value="`+tc.mode+`"]`, chromedp.ByQuery),
				chromedp.Click(`form.theme-picker button[value="`+tc.mode+`"]`, chromedp.ByQuery),
				// Form POST → 303 → GET /login. Sleep briefly so the
				// new page commits before we read attributes.
				chromedp.Sleep(400*time.Millisecond),
				chromedp.WaitVisible(`form.login-form`, chromedp.ByQuery),
				chromedp.AttributeValue("html", "data-theme", &theme, nil),
			); err != nil {
				t.Fatalf("click picker %q: %v", tc.mode, err)
			}
			if theme != tc.wantTheme {
				t.Errorf("after picking %q, data-theme = %q, want %q", tc.mode, theme, tc.wantTheme)
			}

			// Cookie present with the right value + attributes.
			var cookieValue string
			err := chromedp.Run(browserCtx,
				chromedp.ActionFunc(func(ctx context.Context) error {
					cs, err := network.GetCookies().Do(ctx)
					if err != nil {
						return err
					}
					for _, c := range cs {
						if c.Name == "satellites_theme" {
							cookieValue = c.Value
							return nil
						}
					}
					return nil
				}),
			)
			if err != nil {
				t.Fatalf("get cookies: %v", err)
			}
			if cookieValue != tc.mode {
				t.Errorf("satellites_theme cookie = %q, want %q", cookieValue, tc.mode)
			}

			// Reload preserves the choice.
			var reloadedTheme string
			if err := chromedp.Run(browserCtx,
				chromedp.Reload(),
				chromedp.Sleep(200*time.Millisecond),
				chromedp.WaitVisible(`form.login-form`, chromedp.ByQuery),
				chromedp.AttributeValue("html", "data-theme", &reloadedTheme, nil),
			); err != nil {
				t.Fatalf("reload: %v", err)
			}
			if reloadedTheme != tc.wantTheme {
				t.Errorf("after reload, data-theme = %q, want %q", reloadedTheme, tc.wantTheme)
			}
		})
	}
}

// TestThemePicker_SystemMode verifies AC3: when mode=system, the rendered
// theme honours the OS prefers-color-scheme media query.
func TestThemePicker_SystemMode(t *testing.T) {
	h := StartHarness(t)

	parent, cancel := withTimeout(context.Background(), browserDeadline)
	defer cancel()
	browserCtx, cancelBrowser := newChromedpContext(t, parent)
	defer cancelBrowser()

	setMedia := func(value string) chromedp.Action {
		return chromedp.ActionFunc(func(ctx context.Context) error {
			return emulation.SetEmulatedMedia().
				WithFeatures([]*emulation.MediaFeature{{Name: "prefers-color-scheme", Value: value}}).
				Do(ctx)
		})
	}

	// Light system → expect light.
	var theme string
	if err := chromedp.Run(browserCtx,
		network.ClearBrowserCookies(),
		setMedia("light"),
		chromedp.Navigate(h.BaseURL+"/login"),
		chromedp.WaitVisible(`form.theme-picker button[value="system"]`, chromedp.ByQuery),
		chromedp.Click(`form.theme-picker button[value="system"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`form.login-form`, chromedp.ByQuery),
		// Wait briefly for the inline script to apply.
		chromedp.Sleep(150*time.Millisecond),
		chromedp.AttributeValue("html", "data-theme", &theme, nil),
	); err != nil {
		t.Fatalf("system mode (light): %v", err)
	}
	if theme != "light" {
		t.Errorf("system mode + prefers-light: data-theme = %q, want \"light\"", theme)
	}

	// Dark system → expect dark.
	if err := chromedp.Run(browserCtx,
		setMedia("dark"),
		chromedp.Reload(),
		chromedp.WaitVisible(`form.login-form`, chromedp.ByQuery),
		chromedp.Sleep(150*time.Millisecond),
		chromedp.AttributeValue("html", "data-theme", &theme, nil),
	); err != nil {
		t.Fatalf("system mode (dark): %v", err)
	}
	if theme != "dark" {
		t.Errorf("system mode + prefers-dark: data-theme = %q, want \"dark\"", theme)
	}
}

// TestThemePicker_PortalMainCentered verifies AC5: the .portal-main
// element is horizontally centered on the login page (the only page in
// this story that uses the centered primitive — other pages adopt it
// in the landing-page story).
func TestThemePicker_PortalMainCentered(t *testing.T) {
	h := StartHarness(t)

	parent, cancel := withTimeout(context.Background(), browserDeadline)
	defer cancel()
	browserCtx, cancelBrowser := newChromedpContext(t, parent)
	defer cancelBrowser()

	var marginsEqual bool
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(h.BaseURL+"/login"),
		chromedp.WaitVisible(`main.portal-main`, chromedp.ByQuery),
		chromedp.Evaluate(`(function () {
			var el = document.querySelector('main.portal-main');
			var r = el.getBoundingClientRect();
			var leftMargin = r.left;
			var rightMargin = window.innerWidth - r.right;
			return Math.abs(leftMargin - rightMargin) <= 2;
		})()`, &marginsEqual),
	); err != nil {
		t.Fatalf("centered check: %v", err)
	}
	if !marginsEqual {
		t.Errorf(".portal-main is not horizontally centered (left/right margins differ > 2px)")
	}
}

// TestThemePicker_NoFOUCInlineScript verifies AC4: the first-paint inline
// script is positioned inside <head> before any blocking external CSS
// link. We assert the script's source contains the cookie-read function
// AND that the rendered HTML places the script earlier than the
// portal.css <link>.
func TestThemePicker_NoFOUCInlineScript(t *testing.T) {
	h := StartHarness(t)

	parent, cancel := withTimeout(context.Background(), browserDeadline)
	defer cancel()
	browserCtx, cancelBrowser := newChromedpContext(t, parent)
	defer cancelBrowser()

	var html string
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(h.BaseURL+"/login"),
		chromedp.WaitVisible(`form.login-form`, chromedp.ByQuery),
		chromedp.OuterHTML("html", &html, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("navigate /login: %v", err)
	}

	cookieReader := strings.Index(html, "readCookie")
	cssLink := strings.Index(html, "/static/css/portal.css")
	if cookieReader < 0 {
		t.Fatalf("inline first-paint script missing readCookie helper; head html = %s", head(html))
	}
	if cssLink < 0 {
		t.Fatalf("portal.css link missing from page head")
	}
	if cookieReader > cssLink {
		t.Errorf("inline first-paint script (offset %d) appears AFTER portal.css link (offset %d) — would FOUC", cookieReader, cssLink)
	}
}

func head(s string) string {
	if len(s) > 1024 {
		return s[:1024]
	}
	return s
}
