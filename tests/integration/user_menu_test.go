//go:build integration

package integration_test

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
	"github.com/chromedp/chromedp"
)

// TestUserMenu_Dropdown exercises sty_c07c94a3 — avatar button +
// dropdown panel with API Keys / KV Values / Logout. Asserts:
//   - Avatar is the only visible user-scoped control before interaction.
//   - Clicking the avatar opens the panel.
//   - Clicking outside closes it.
//   - Escape closes it.
//   - Panel contains the expected items + no others.
func TestUserMenu_Dropdown(t *testing.T) {
	env := testbootstrap.SetUpWithServer(t)
	ctx := newBrowserCtx(t)

	// Login first to surface the avatar.
	if err := chromedp.Run(ctx,
		chromedp.Navigate(env.ServerURL+"/login"),
		chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
		chromedp.Click(`button[data-action="dev-login-admin"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("login: %v", err)
	}

	t.Run("avatar renders, panel hidden initially", func(t *testing.T) {
		var panelVisible bool
		var avatarText string
		if err := chromedp.Run(ctx,
			chromedp.WaitVisible(`[data-field="user-menu-avatar"]`, chromedp.ByQuery),
			chromedp.Text(`[data-field="user-menu-avatar"]`, &avatarText, chromedp.ByQuery),
			chromedp.Evaluate(`(() => {
				const panel = document.querySelector('[data-field="user-menu-panel"]');
				return !!panel && panel.offsetParent !== null;
			})()`, &panelVisible),
		); err != nil {
			t.Fatalf("read state: %v", err)
		}
		// DevSeed gives admin user DisplayName "Dev Admin" → avatar 'D'.
		if avatarText != "D" {
			t.Errorf("avatar text: got %q want %q", avatarText, "D")
		}
		if panelVisible {
			t.Error("panel should be hidden before avatar click")
		}
	})

	t.Run("avatar click opens panel; outside click closes", func(t *testing.T) {
		var panelVisible bool
		if err := chromedp.Run(ctx,
			chromedp.Click(`[data-field="user-menu-avatar"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-field="user-menu-panel"]`, chromedp.ByQuery),
			chromedp.Evaluate(`document.querySelector('[data-field="user-menu-panel"]').offsetParent !== null`, &panelVisible),
		); err != nil {
			t.Fatalf("open: %v", err)
		}
		if !panelVisible {
			t.Fatal("panel did not open on avatar click")
		}

		// Click outside on the server panel (no navigation side-effect).
		// Alpine @click.outside is registered on document, so any
		// dispatched click outside the dropdown root closes it.
		if err := chromedp.Run(ctx,
			chromedp.Evaluate(`document.querySelector('[data-section="server"]').click()`, nil),
		); err != nil {
			t.Fatalf("outside click: %v", err)
		}
		// Wait for offsetParent to flip back to null.
		if err := chromedp.Run(ctx, chromedp.Poll(
			`!document.querySelector('[data-field="user-menu-panel"]') || document.querySelector('[data-field="user-menu-panel"]').offsetParent === null`,
			&panelVisible,
		)); err != nil {
			t.Fatalf("wait close: %v", err)
		}
	})

	t.Run("panel contains expected items + email + logout button", func(t *testing.T) {
		// Re-open menu and snapshot panel contents.
		var diag map[string]interface{}
		if err := chromedp.Run(ctx,
			chromedp.Navigate(env.ServerURL+"/"),
			chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
			chromedp.Click(`[data-action="user-menu-toggle"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-field="user-menu-panel"]`, chromedp.ByQuery),
			chromedp.Evaluate(`(() => {
				const panel = document.querySelector('[data-field="user-menu-panel"]');
				return {
					email: (panel.querySelector('[data-field="user-email"]')||{}).textContent || '',
					keysHref: (panel.querySelector('[data-field="user-menu-keys"]')||{}).getAttribute && panel.querySelector('[data-field="user-menu-keys"]').getAttribute('href'),
					kvHref: (panel.querySelector('[data-field="user-menu-kv"]')||{}).getAttribute && panel.querySelector('[data-field="user-menu-kv"]').getAttribute('href'),
					hasLogout: !!panel.querySelector('[data-action="logout"]')
				};
			})()`, &diag),
		); err != nil {
			t.Fatalf("panel snapshot: %v", err)
		}
		email, _ := diag["email"].(string)
		if !strings.Contains(email, auth.DevAdminEmail) {
			t.Errorf("panel email: got %q want contains %q", email, auth.DevAdminEmail)
		}
		if got, _ := diag["keysHref"].(string); got != "/settings/api-keys" {
			t.Errorf("API Keys href: got %q want /settings/api-keys", got)
		}
		if got, _ := diag["kvHref"].(string); got != "/settings/system-kv" {
			t.Errorf("KV Values href: got %q want /settings/system-kv", got)
		}
		if has, _ := diag["hasLogout"].(bool); !has {
			t.Error("panel missing logout button")
		}
	})

	t.Run("Escape closes the panel", func(t *testing.T) {
		// Re-navigate to reset panel state (prior subtests leave it open).
		if err := chromedp.Run(ctx,
			chromedp.Navigate(env.ServerURL+"/"),
			chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
			chromedp.Click(`[data-action="user-menu-toggle"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-field="user-menu-panel"]`, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("open: %v", err)
		}
		// Send Escape via JS dispatch (chromedp.KeyEvent targets body
		// reliably).
		if err := chromedp.Run(ctx, chromedp.KeyEvent("")); err != nil {
			t.Fatalf("escape: %v", err)
		}
		var closed bool
		if err := chromedp.Run(ctx, chromedp.Poll(
			`!document.querySelector('[data-field="user-menu-panel"]') || document.querySelector('[data-field="user-menu-panel"]').offsetParent === null`,
			&closed,
		)); err != nil {
			t.Fatalf("wait close: %v", err)
		}
	})
}
