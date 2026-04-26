//go:build portalui

package portalui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// TestNavHamburger_HoverNonInverting covers story_cffd92d4 AC1: hovering
// the hamburger button on dark theme must not flip the background to
// white. The fix keeps the button transparent and lifts only the border.
func TestNavHamburger_HoverNonInverting(t *testing.T) {
	h := StartHarness(t)

	parent, cancel := withTimeout(context.Background(), browserDeadline)
	defer cancel()
	browserCtx, cancelBrowser := newChromedpContext(t, parent)
	defer cancelBrowser()

	if err := installSessionCookie(browserCtx, h); err != nil {
		t.Fatalf("install session cookie: %v", err)
	}

	// Read computed background BEFORE hover, then after dispatching a
	// mouseover event. The post-hover background must not be the dark
	// theme's foreground colour (which would visually flash white).
	const readHover = `(() => {
		const btn = document.querySelector('button[data-testid="nav-hamburger"]');
		if (!btn) { return ['MISSING', 'MISSING']; }
		btn.dispatchEvent(new MouseEvent('mouseover', {bubbles: true}));
		btn.dispatchEvent(new MouseEvent('mouseenter', {bubbles: true}));
		// Force :hover via direct style query — chromedp lacks a
		// trustworthy :hover toggle, so fall back to a CSSOM lookup of
		// the matching :hover rule.
		const sheets = Array.from(document.styleSheets);
		for (const s of sheets) {
			let rules;
			try { rules = s.cssRules; } catch { continue; }
			if (!rules) { continue; }
			for (const r of rules) {
				if (r.selectorText && r.selectorText === '.nav-hamburger:hover') {
					return [
						r.style.backgroundColor,
						r.style.color,
					];
				}
			}
		}
		return ['NOT-FOUND', 'NOT-FOUND'];
	})()`

	var hoverState [2]string
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(h.BaseURL+"/"),
		chromedp.WaitVisible(`button[data-testid="nav-hamburger"]`, chromedp.ByQuery),
		chromedp.Sleep(300*time.Millisecond),
		chromedp.Evaluate(readHover, &hoverState),
	); err != nil {
		t.Fatalf("read hover: %v", err)
	}

	// AC1: hover backgroundColor must NOT be the colour-fg variable's
	// resolved value. The simplest invariant: it must not equal common
	// dark-theme fg values (#fff, white, #e8e8e8) and ideally is
	// transparent/empty.
	bg := strings.ToLower(strings.TrimSpace(hoverState[0]))
	for _, banned := range []string{"#fff", "#ffffff", "white", "rgb(255, 255, 255)", "var(--color-fg)"} {
		if bg == banned {
			t.Errorf("AC1: .nav-hamburger:hover backgroundColor = %q; should not invert to fg colour. Full hover state: bg=%q color=%q", bg, hoverState[0], hoverState[1])
		}
	}
	if bg == "" || bg == "transparent" || strings.HasPrefix(bg, "rgba(0, 0, 0, 0)") {
		// PASS: explicitly transparent or empty.
		return
	}
	// Otherwise allow non-inverting non-transparent values — we just
	// don't want a white flash. Document the value for grep-ability.
	t.Logf("hamburger hover bg=%q color=%q (non-inverting)", hoverState[0], hoverState[1])
}

// TestPortalCSP_NoImageViolations covers story_cffd92d4 AC3: navigating
// to pages with <select> elements (which use data:image/svg+xml URLs
// for the dropdown caret) must NOT report CSP image-load violations.
//
// Captures both runtime exceptions and Log domain entries — Chromium
// reports CSP violations through the Log domain.
func TestPortalCSP_NoImageViolations(t *testing.T) {
	h := StartHarness(t)

	parent, cancel := withTimeout(context.Background(), browserDeadline)
	defer cancel()
	browserCtx, cancelBrowser := newChromedpContext(t, parent)
	defer cancelBrowser()

	if err := installSessionCookie(browserCtx, h); err != nil {
		t.Fatalf("install session cookie: %v", err)
	}

	var (
		mu         sync.Mutex
		violations []string
	)
	chromedp.ListenTarget(browserCtx, func(ev interface{}) {
		switch e := ev.(type) {
		case *log.EventEntryAdded:
			if e.Entry == nil {
				return
			}
			text := e.Entry.Text
			if strings.Contains(text, "Content Security Policy") &&
				strings.Contains(text, "data:image/svg+xml") {
				mu.Lock()
				violations = append(violations, text)
				mu.Unlock()
			}
		case *runtime.EventExceptionThrown:
			if e.ExceptionDetails == nil {
				return
			}
			text := e.ExceptionDetails.Text
			if e.ExceptionDetails.Exception != nil {
				text += " " + e.ExceptionDetails.Exception.Description
			}
			if strings.Contains(text, "Content Security Policy") &&
				strings.Contains(text, "data:image/svg+xml") {
				mu.Lock()
				violations = append(violations, text)
				mu.Unlock()
			}
		}
	})

	if err := chromedp.Run(browserCtx,
		log.Enable(),
		runtime.Enable(),
		chromedp.Navigate(h.BaseURL+"/tasks"),
		chromedp.WaitVisible(`section[data-testid="tasks-header"]`, chromedp.ByQuery),
		chromedp.Sleep(800*time.Millisecond),
	); err != nil {
		t.Fatalf("navigate /tasks: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(violations) > 0 {
		t.Errorf("CSP blocked %d data:image/svg+xml load(s) on /tasks; first violation: %s", len(violations), violations[0])
	}
}

// TestNav_NoStrayIconBelowHamburger covers story_cffd92d4 AC4: the area
// directly below the hamburger button on the authed dashboard must
// contain no rendered images / icons / broken-image placeholders.
//
// Implementation: grab the hamburger's bounding rect, then enumerate
// every element in the body whose bounding rect overlaps a rectangle
// from (hamburgerLeft, hamburgerBottom) to (hamburgerRight, hamburgerBottom+50).
// Any <img>, <svg>, or element with a non-empty `background-image`
// computed style inside that region is reported. Only the hamburger
// button itself + its descendant icon span are tolerated.
func TestNav_NoStrayIconBelowHamburger(t *testing.T) {
	h := StartHarness(t)

	parent, cancel := withTimeout(context.Background(), browserDeadline)
	defer cancel()
	browserCtx, cancelBrowser := newChromedpContext(t, parent)
	defer cancelBrowser()

	if err := installSessionCookie(browserCtx, h); err != nil {
		t.Fatalf("install session cookie: %v", err)
	}

	const probe = `(() => {
		const btn = document.querySelector('button[data-testid="nav-hamburger"]');
		if (!btn) { return {error: 'hamburger missing'}; }
		const r = btn.getBoundingClientRect();
		const region = {left: r.left - 4, right: r.right + 4, top: r.bottom, bottom: r.bottom + 60};
		const matches = [];
		const all = document.querySelectorAll('img, svg, [style*="background-image"]');
		for (const el of all) {
			const er = el.getBoundingClientRect();
			if (er.right < region.left || er.left > region.right) { continue; }
			if (er.bottom < region.top || er.top > region.bottom) { continue; }
			matches.push({tag: el.tagName, id: el.id, cls: el.className, rect: [er.left, er.top, er.width, er.height]});
		}
		// Also enumerate every element with a non-empty computed
		// background-image (catches the data:image/svg+xml caret on
		// selects and similar) inside the region.
		const everyEl = document.querySelectorAll('body *');
		for (const el of everyEl) {
			const cs = getComputedStyle(el);
			if (cs.backgroundImage && cs.backgroundImage !== 'none') {
				const er = el.getBoundingClientRect();
				if (er.right < region.left || er.left > region.right) { continue; }
				if (er.bottom < region.top || er.top > region.bottom) { continue; }
				matches.push({tag: el.tagName, id: el.id, cls: el.className, rect: [er.left, er.top, er.width, er.height], bg: cs.backgroundImage.slice(0, 80)});
			}
		}
		return {region, matches};
	})()`

	var result struct {
		Region  map[string]float64       `json:"region"`
		Matches []map[string]interface{} `json:"matches"`
		Error   string                   `json:"error"`
	}
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(h.BaseURL+"/"),
		chromedp.WaitVisible(`button[data-testid="nav-hamburger"]`, chromedp.ByQuery),
		chromedp.Sleep(400*time.Millisecond),
		chromedp.Evaluate(probe, &result),
	); err != nil {
		t.Fatalf("probe stray icon: %v", err)
	}

	if result.Error != "" {
		t.Fatalf("probe error: %s", result.Error)
	}
	if len(result.Matches) > 0 {
		t.Errorf("AC4: %d element(s) with image content found in the region directly below the hamburger on the authed dashboard. The dashboard has no <select> / <img> by design; any match here is a regression. matches=%+v region=%+v", len(result.Matches), result.Matches, result.Region)
	}
}
