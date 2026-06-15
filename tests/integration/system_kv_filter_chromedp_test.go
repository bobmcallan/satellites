//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/variable"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
	"github.com/chromedp/chromedp"
)

// TestSystemKVPage_Chromedp drives the /settings/system-kv page (sty_6c207c12):
// the key-values render as ONE table with a SCOPE column spanning system + the
// caller's user-scope overrides, and the name filter narrows the rendered rows
// as you type (clearing restores the full list).
func TestSystemKVPage_Chromedp(t *testing.T) {
	env := testbootstrap.SetUpWithServer(t)

	varStore := variable.New(env.DB)
	verb.SetAuthStore(env.Store)
	verb.SetVariableStore(varStore)
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetVariableStore(nil)
	})

	ctx := context.Background()
	now := time.Now().UTC()

	// Two stored system knobs.
	for _, p := range [][2]string{{"stories.page_size", "50"}, {"feature.flag.x", "on"}} {
		if _, err := varStore.SeedSystem(ctx, p[0], p[1], now); err != nil {
			t.Fatalf("seed %s: %v", p[0], err)
		}
	}

	// A user-scope override owned by the dev admin (the chromedp session user),
	// so the SCOPE column spans more than one layer.
	admin, err := env.Store.GetUserByEmail(ctx, auth.DevAdminEmail)
	if err != nil {
		t.Fatalf("lookup admin: %v", err)
	}
	if _, err := verb.Dispatch(authWithUser(ctx, admin), "variable_set", json.RawMessage(
		`{"name":"my.pref","scope":"user","value":"hello"}`)); err != nil {
		t.Fatalf("set user override: %v", err)
	}

	bctx := newBrowserCtx(t)
	if err := chromedp.Run(bctx,
		chromedp.Navigate(env.ServerURL+"/login"),
		chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
		chromedp.Click(`button[data-action="dev-login-admin"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
		chromedp.Navigate(env.ServerURL+"/settings/system-kv"),
		chromedp.WaitVisible(`[data-field="system-kv-table"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("login + navigate to /settings/system-kv: %v", err)
	}

	// Count rows currently VISIBLE (a client-side x-show filter leaves hidden
	// rows in the DOM with offsetParent == null).
	visible := func() int {
		var n int
		if err := chromedp.Run(bctx, chromedp.Evaluate(
			`[...document.querySelectorAll('[data-field="kv-row"]')].filter(r => r.offsetParent !== null).length`, &n)); err != nil {
			t.Fatalf("count visible: %v", err)
		}
		return n
	}

	// AC1: a SCOPE column spanning system + user.
	var scopes []string
	if err := chromedp.Run(bctx, chromedp.Evaluate(
		`[...new Set([...document.querySelectorAll('[data-field="kv-row"]')].map(r => r.dataset.kvScope))].sort()`, &scopes)); err != nil {
		t.Fatalf("scope set: %v", err)
	}
	if len(scopes) != 2 || scopes[0] != "system" || scopes[1] != "user" {
		t.Fatalf("scope column should span system + user, got %v", scopes)
	}

	// All three rows visible initially (2 system + 1 user).
	if n := visible(); n != 3 {
		t.Fatalf("initial visible rows = %d, want 3", n)
	}

	// AC2: typing a name narrows the rendered rows.
	if err := chromedp.Run(bctx,
		chromedp.SendKeys(`[data-field="system-kv-search"]`, "stories", chromedp.ByQuery),
		chromedp.Sleep(400*time.Millisecond),
	); err != nil {
		t.Fatalf("type filter: %v", err)
	}
	if n := visible(); n != 1 {
		t.Fatalf("after filter 'stories': visible rows = %d, want 1", n)
	}

	// Clearing the filter restores the full list. Set the value and dispatch a
	// real input event so Alpine's x-model reacts (SetValue alone may not).
	var cleared bool
	if err := chromedp.Run(bctx,
		chromedp.Evaluate(`(() => { const el = document.querySelector('[data-field="system-kv-search"]'); el.value = ''; el.dispatchEvent(new Event('input', {bubbles:true})); return true; })()`, &cleared),
		chromedp.Sleep(400*time.Millisecond),
	); err != nil {
		t.Fatalf("clear filter: %v", err)
	}
	if n := visible(); n != 3 {
		t.Fatalf("after clearing filter: visible rows = %d, want 3", n)
	}
}
