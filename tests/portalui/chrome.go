//go:build portalui

package portalui

import (
	"context"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"github.com/bobmcallan/satellites/internal/auth"
)

// browserDeadline is the per-test cap on chromedp work. Tests still wrap
// individual flows in shorter context.WithTimeout windows.
const browserDeadline = 60 * time.Second

// newChromedpContext spins up a headless Chromium and probes it. When no
// Chrome binary is reachable (CI without chromium installed, sandbox
// configurations that block Chrome's seccomp probe) the call falls back
// to t.Skipf — contributors without chromium don't see a hard fail.
//
// The returned cancel func tears down the chromedp context and the
// underlying ExecAllocator. Tests defer it.
func newChromedpContext(t *testing.T, parent context.Context) (context.Context, context.CancelFunc) {
	t.Helper()

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(parent, opts...)
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)

	// Force the browser to actually launch so we know whether chromium is
	// reachable before the test's first WaitVisible. A naked chromedp.Run
	// with no actions starts the browser then exits; on a missing binary
	// it returns an os/exec error inside chromedp's allocator.
	if err := chromedp.Run(browserCtx); err != nil {
		cancelBrowser()
		cancelAlloc()
		t.Skipf("chromium unavailable: %v", err)
	}

	cancel := func() {
		cancelBrowser()
		cancelAlloc()
	}
	return browserCtx, cancel
}

// installSessionCookie writes the session cookie under the harness's
// origin so the next navigation lands authed without driving the login
// form. Auth flow coverage already lives in tests/integration/portal_test.go.
func installSessionCookie(ctx context.Context, h *Harness) error {
	u, err := url.Parse(h.BaseURL)
	if err != nil {
		return fmt.Errorf("parse base url: %w", err)
	}
	expires := cdp.TimeSinceEpoch(time.Now().Add(time.Hour))
	return chromedp.Run(ctx,
		network.SetCookie(auth.CookieName, h.SessionCookieValue).
			WithDomain(u.Hostname()).
			WithPath("/").
			WithHTTPOnly(true).
			WithExpires(&expires),
	)
}

// installFastFlag arranges for `window.__SATELLITES_WS_FAST = true` to
// run BEFORE any in-page script via DevTools' addScriptToEvaluateOnNewDocument.
// ws.js consults the flag at module load and uses compressed backoff
// constants (50/200/30 ms) so tests can reach the disconnected state in
// seconds. Production behaviour is unaffected when the flag is absent.
func installFastFlag(ctx context.Context) error {
	return chromedp.Run(ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, err := page.AddScriptToEvaluateOnNewDocument(`window.__SATELLITES_WS_FAST = true;`).Do(ctx)
			return err
		}),
	)
}

// waitForIndicatorState polls the widget's class list until it carries
// the requested ws-indicator-<state> class, or the deadline elapses.
func waitForIndicatorState(ctx context.Context, state string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	target := "ws-indicator-" + state
	for time.Now().Before(deadline) {
		var classes string
		err := chromedp.Run(ctx,
			chromedp.AttributeValue(".ws-indicator", "class", &classes, nil),
		)
		if err == nil && containsClassToken(classes, target) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return fmt.Errorf("timed out waiting for indicator state %q", state)
}

// jsClick fires a synthetic click on the first node matching the
// selector via `HTMLElement.click()`, dispatched as a JS-level click
// event. Required because @alpinejs/csp@3.14.9 (introduced by
// story_739823eb) does not invoke Alpine's `@click="handler"` binding
// for chromedp's CDP-injected Input.dispatchMouseEvent — only synthetic
// JS events fire the handler. Real user clicks in production browsers
// are unaffected; this is purely a chromedp ↔ @alpinejs/csp interaction
// quirk. Use this helper everywhere a chromedp test needs to drive an
// `@click` handler.
func jsClick(selector string) chromedp.Action {
	expr := `(() => {
		const el = document.querySelector(` + jsString(selector) + `);
		if (!el) { return false; }
		el.click();
		return true;
	})()`
	return chromedp.ActionFunc(func(ctx context.Context) error {
		var ok bool
		if err := chromedp.Run(ctx, chromedp.Evaluate(expr, &ok)); err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("jsClick: no element matched selector %q", selector)
		}
		return nil
	})
}

// jsString returns a JS string literal safely encoding s (single quotes
// + escapes). Used by jsClick so callers can pass a raw CSS selector.
func jsString(s string) string {
	var b []byte
	b = append(b, '\'')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\', '\'':
			b = append(b, '\\', c)
		case '\n':
			b = append(b, '\\', 'n')
		case '\r':
			b = append(b, '\\', 'r')
		default:
			b = append(b, c)
		}
	}
	b = append(b, '\'')
	return string(b)
}

// containsClassToken reports whether `class` (a whitespace-separated
// class attribute value) carries `target` as a complete token.
func containsClassToken(class, target string) bool {
	n := len(target)
	for i := 0; i+n <= len(class); i++ {
		if i > 0 && !isClassSep(class[i-1]) {
			continue
		}
		if i+n < len(class) && !isClassSep(class[i+n]) {
			continue
		}
		if class[i:i+n] == target {
			return true
		}
	}
	return false
}

func isClassSep(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }
