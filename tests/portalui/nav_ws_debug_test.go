//go:build portalui

package portalui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

// TestNavWSDebug_HiddenByDefault covers story_ce02d0d9: the ws-debug
// panel ("no events yet" empty state) must not be visible on first paint
// and must respond to dot toggle + outside-click. The user-reported
// "always on" symptom did not reproduce in the harness; this test
// captures the correct behaviour as a regression guard.
func TestNavWSDebug_HiddenByDefault(t *testing.T) {
	h := StartHarness(t)

	parent, cancel := withTimeout(context.Background(), browserDeadline)
	defer cancel()
	browserCtx, cancelBrowser := newChromedpContext(t, parent)
	defer cancelBrowser()

	if err := installSessionCookie(browserCtx, h); err != nil {
		t.Fatalf("install session cookie: %v", err)
	}

	const wsDebugDisplay = `(() => {
		const el = document.querySelector('.ws-debug');
		return el ? getComputedStyle(el).display : 'NOT-FOUND';
	})()`
	const emptyVisible = `(() => {
		const el = document.querySelector('.ws-debug-empty');
		if (!el) { return false; }
		const r = el.getBoundingClientRect();
		return getComputedStyle(el).display !== 'none' && r.width > 0 && r.height > 0;
	})()`
	const bodyClick = `(() => {
		const evt = new MouseEvent('click', {bubbles: true, cancelable: true, view: window, clientX: 50, clientY: 400});
		document.body.dispatchEvent(evt);
	})()`

	t.Run("no_debug_param panel never renders dropdown content", func(t *testing.T) {
		// Without ?debug=true the panel content should never be reachable;
		// toggleDebug() in ws.js short-circuits unless debug is true. The
		// panel's empty-state must not be visible regardless.
		var dispOnLoad string
		var emptyVisOnLoad bool
		if err := chromedp.Run(browserCtx,
			chromedp.Navigate(h.BaseURL+"/"),
			chromedp.WaitVisible(".ws-indicator", chromedp.ByQuery),
			chromedp.Sleep(300*time.Millisecond),
			chromedp.Evaluate(wsDebugDisplay, &dispOnLoad),
			chromedp.Evaluate(emptyVisible, &emptyVisOnLoad),
		); err != nil {
			t.Fatalf("navigate /: %v", err)
		}
		if dispOnLoad != "none" {
			t.Errorf("AC1: ws-debug computedDisplay=%q on first paint, want 'none'", dispOnLoad)
		}
		if emptyVisOnLoad {
			t.Errorf("AC3-text: 'no events yet' visible on first paint with no debug param; expected hidden")
		}
	})

	t.Run("debug_param hidden_on_load", func(t *testing.T) {
		var dispOnLoad string
		var emptyVisOnLoad bool
		if err := chromedp.Run(browserCtx,
			chromedp.Navigate(h.BaseURL+"/?debug=true"),
			chromedp.WaitVisible(".ws-indicator", chromedp.ByQuery),
			chromedp.Sleep(300*time.Millisecond),
			chromedp.Evaluate(wsDebugDisplay, &dispOnLoad),
			chromedp.Evaluate(emptyVisible, &emptyVisOnLoad),
		); err != nil {
			t.Fatalf("navigate /?debug=true: %v", err)
		}
		if dispOnLoad != "none" {
			t.Errorf("AC1: ws-debug computedDisplay=%q on first paint with debug=true, want 'none'", dispOnLoad)
		}
		if emptyVisOnLoad {
			t.Errorf("AC3-text: 'no events yet' visible on first paint with debug=true; expected hidden")
		}
	})

	t.Run("debug_param dot_toggle_open_close", func(t *testing.T) {
		var afterOpen, afterClose string
		if err := chromedp.Run(browserCtx,
			chromedp.Navigate(h.BaseURL+"/?debug=true"),
			chromedp.WaitVisible(".ws-indicator", chromedp.ByQuery),
			chromedp.Sleep(200*time.Millisecond),
			chromedp.Click(".ws-indicator .ws-indicator-btn", chromedp.ByQuery),
			chromedp.Sleep(200*time.Millisecond),
			chromedp.Evaluate(wsDebugDisplay, &afterOpen),
			chromedp.Click(".ws-indicator .ws-indicator-btn", chromedp.ByQuery),
			chromedp.Sleep(200*time.Millisecond),
			chromedp.Evaluate(wsDebugDisplay, &afterClose),
		); err != nil {
			t.Fatalf("dot toggle: %v", err)
		}
		if afterOpen == "none" {
			t.Errorf("AC2: ws-debug not visible after dot click, computedDisplay=%q", afterOpen)
		}
		if afterClose != "none" {
			t.Errorf("AC2: ws-debug still visible after second dot click, computedDisplay=%q", afterClose)
		}
	})

	t.Run("debug_param body_click_closes", func(t *testing.T) {
		var afterOpen, afterBody string
		if err := chromedp.Run(browserCtx,
			chromedp.Navigate(h.BaseURL+"/?debug=true"),
			chromedp.WaitVisible(".ws-indicator", chromedp.ByQuery),
			chromedp.Sleep(200*time.Millisecond),
			chromedp.Click(".ws-indicator .ws-indicator-btn", chromedp.ByQuery),
			chromedp.Sleep(200*time.Millisecond),
			chromedp.Evaluate(wsDebugDisplay, &afterOpen),
			chromedp.Evaluate(bodyClick, nil),
			chromedp.Sleep(200*time.Millisecond),
			chromedp.Evaluate(wsDebugDisplay, &afterBody),
		); err != nil {
			t.Fatalf("body-click close: %v", err)
		}
		if afterOpen == "none" {
			t.Fatalf("AC3 setup: panel didn't open, computedDisplay=%q", afterOpen)
		}
		if afterBody != "none" {
			t.Errorf("AC3: ws-debug still visible after body click, computedDisplay=%q", afterBody)
		}
	})

	t.Run("debug_param escape_closes", func(t *testing.T) {
		var afterOpen, afterEsc string
		if err := chromedp.Run(browserCtx,
			chromedp.Navigate(h.BaseURL+"/?debug=true"),
			chromedp.WaitVisible(".ws-indicator", chromedp.ByQuery),
			chromedp.Sleep(200*time.Millisecond),
			chromedp.Click(".ws-indicator .ws-indicator-btn", chromedp.ByQuery),
			chromedp.Sleep(200*time.Millisecond),
			chromedp.Evaluate(wsDebugDisplay, &afterOpen),
			chromedp.KeyEvent(kb.Escape),
			chromedp.Sleep(200*time.Millisecond),
			chromedp.Evaluate(wsDebugDisplay, &afterEsc),
		); err != nil {
			t.Fatalf("escape close: %v", err)
		}
		if afterOpen == "none" {
			t.Fatalf("setup: panel didn't open, computedDisplay=%q", afterOpen)
		}
		if afterEsc != "none" {
			t.Errorf("ws-debug still visible after Escape, computedDisplay=%q", afterEsc)
		}
	})

	t.Run("debug_param hamburger_click_closes_ws_debug", func(t *testing.T) {
		// Regression guard for the user-reported pattern: panel is open AND
		// dropdown is open simultaneously. Clicking hamburger to open the
		// menu must close the ws-debug panel via @click.outside on .ws-debug.
		var afterOpen, afterHamburger, dropdownDisp string
		if err := chromedp.Run(browserCtx,
			chromedp.Navigate(h.BaseURL+"/?debug=true"),
			chromedp.WaitVisible(".ws-indicator", chromedp.ByQuery),
			chromedp.Sleep(200*time.Millisecond),
			chromedp.Click(".ws-indicator .ws-indicator-btn", chromedp.ByQuery),
			chromedp.Sleep(200*time.Millisecond),
			chromedp.Evaluate(wsDebugDisplay, &afterOpen),
			chromedp.Click(`button[data-testid="nav-hamburger"]`, chromedp.ByQuery),
			chromedp.Sleep(200*time.Millisecond),
			chromedp.Evaluate(wsDebugDisplay, &afterHamburger),
			chromedp.Evaluate(`(() => { const el = document.querySelector('[data-testid="nav-dropdown"]'); return el ? getComputedStyle(el).display : 'NOT-FOUND'; })()`, &dropdownDisp),
		); err != nil {
			t.Fatalf("hamburger-close ws-debug: %v", err)
		}
		if afterOpen == "none" {
			t.Fatalf("setup: panel didn't open, computedDisplay=%q", afterOpen)
		}
		if afterHamburger != "none" {
			t.Errorf("ws-debug still visible after hamburger click, computedDisplay=%q (regression: simultaneous menu+ws-debug)", afterHamburger)
		}
		if dropdownDisp == "none" || strings.TrimSpace(dropdownDisp) == "" {
			t.Errorf("hamburger dropdown should be open after click, computedDisplay=%q", dropdownDisp)
		}
	})
}
