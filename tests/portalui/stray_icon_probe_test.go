//go:build portalui

package portalui

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"net/url"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestNav_StrayIconProbe_AllPages covers story_36e2b2ea: enumerate every
// image-bearing element in a 60px band below the hamburger across the
// authed pages where the user has reported regressions.
//
// On a regression, the failure output names every offending element so
// the source CSS rule / template fragment is identifiable from CI logs
// without re-running interactively.
func TestNav_StrayIconProbe_AllPages(t *testing.T) {
	pages := []string{"/", "/tasks", "/projects", "/settings"}

	for _, page := range pages {
		page := page
		t.Run("page="+page, func(t *testing.T) {
			h := StartHarness(t)

			parent, cancel := withTimeout(context.Background(), browserDeadline)
			defer cancel()
			browserCtx, cancelBrowser := newChromedpContext(t, parent)
			defer cancelBrowser()

			if err := installSessionCookie(browserCtx, h); err != nil {
				t.Fatalf("install session cookie: %v", err)
			}

			// Force dark theme so the dark-theme rules participate (the
			// reported screenshot is in dark mode).
			u, err := url.Parse(h.BaseURL)
			if err != nil {
				t.Fatalf("parse base url: %v", err)
			}
			expires := cdp.TimeSinceEpoch(time.Now().Add(time.Hour))
			if err := chromedp.Run(browserCtx,
				network.SetCookie("satellites_theme", "dark").
					WithDomain(u.Hostname()).
					WithPath("/").
					WithExpires(&expires),
			); err != nil {
				t.Fatalf("set theme cookie: %v", err)
			}

			// Probe: elements + non-empty text nodes whose rendered rect
			// intersects a 60px band below the hamburger button. Story
			// 36e2b2ea: the previous probe missed text-content overflow
			// (the literal `≡` character inside .nav-hamburger-icon
			// rendered BELOW the CSS-drawn icon because the span's
			// height was 2px while the character's text height was
			// ~16px). Now we also flag any element whose visible box
			// extends into the band — image or not.
			const probe = `(() => {
				const btn = document.querySelector('button[data-testid="nav-hamburger"]');
				if (!btn) { return {error: 'hamburger missing'}; }
				const r = btn.getBoundingClientRect();
				const region = {left: r.left - 4, right: r.right + 4, top: r.bottom, bottom: r.bottom + 60};
				const matches = [];
				const everyEl = document.querySelectorAll('body *');
				for (const el of everyEl) {
					if (el === btn) { continue; }
					if (btn.contains(el)) { continue; }
					// Skip the dropdown — it sits in this band by design when open;
					// we run with it closed, but still scope the check to non-dropdown.
					if (el.closest('[data-testid="nav-dropdown"]')) { continue; }
					const cs = getComputedStyle(el);
					if (cs.display === 'none' || cs.visibility === 'hidden') { continue; }
					const er = el.getBoundingClientRect();
					if (er.right < region.left || er.left > region.right) { continue; }
					if (er.bottom < region.top || er.top > region.bottom) { continue; }
					if (er.width === 0 || er.height === 0) { continue; }
					const isImg = el.tagName === 'IMG' || el.tagName === 'SVG';
					const hasBg = cs.backgroundImage && cs.backgroundImage !== 'none';
					const ownText = Array.from(el.childNodes)
						.filter(n => n.nodeType === Node.TEXT_NODE && n.textContent.trim().length > 0)
						.map(n => n.textContent.trim()).join(' ');
					if (!isImg && !hasBg && ownText === '') { continue; }
					matches.push({
						tag: el.tagName,
						id: el.id,
						cls: el.className,
						rect: [Math.round(er.left), Math.round(er.top), Math.round(er.width), Math.round(er.height)],
						bg: hasBg ? cs.backgroundImage.slice(0, 100) : '',
						text: ownText.slice(0, 40),
					});
				}
				return {region: [Math.round(region.left), Math.round(region.top), Math.round(region.right), Math.round(region.bottom)], matches};
			})()`

			var result struct {
				Region  []int                    `json:"region"`
				Matches []map[string]interface{} `json:"matches"`
				Error   string                   `json:"error"`
			}
			if err := chromedp.Run(browserCtx,
				chromedp.Navigate(h.BaseURL+page),
				chromedp.WaitVisible(`button[data-testid="nav-hamburger"]`, chromedp.ByQuery),
				chromedp.Sleep(500*time.Millisecond),
				chromedp.Evaluate(probe, &result),
			); err != nil {
				t.Fatalf("probe %s: %v", page, err)
			}
			if result.Error != "" {
				t.Fatalf("probe error: %s", result.Error)
			}
			if len(result.Matches) > 0 {
				dump, _ := json.MarshalIndent(result.Matches, "", "  ")
				t.Errorf("AC1: %d image-bearing element(s) below the hamburger on %s. region=%v matches:\n%s",
					len(result.Matches), page, result.Region, string(dump))
			}

			// Pixel-sample probe — covers text overflow (the literal `≡`
			// inside .nav-hamburger-icon would render below the CSS bars
			// but its BCR stays at 22x2, so the DOM probe above misses
			// it). Sample a 5x5 pixel grid in the band; any non-bg pixel
			// is a paint we don't expect.
			var bandRect [4]float64
			var bgRGB [3]int
			if err := chromedp.Run(browserCtx,
				chromedp.Evaluate(`(() => {
					const btn = document.querySelector('button[data-testid="nav-hamburger"]');
					const r = btn.getBoundingClientRect();
					return [r.left, r.bottom + 4, r.right, r.bottom + 50];
				})()`, &bandRect),
				chromedp.Evaluate(`(() => {
					const c = getComputedStyle(document.body).backgroundColor;
					const m = c.match(/rgba?\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)/);
					if (!m) { return [0, 0, 0]; }
					return [parseInt(m[1]), parseInt(m[2]), parseInt(m[3])];
				})()`, &bgRGB),
			); err != nil {
				t.Fatalf("read band rect: %v", err)
			}

			var screenshot []byte
			if err := chromedp.Run(browserCtx,
				chromedp.FullScreenshot(&screenshot, 100),
			); err != nil {
				t.Fatalf("screenshot: %v", err)
			}
			img, err := png.Decode(bytes.NewReader(screenshot))
			if err != nil {
				t.Fatalf("decode screenshot: %v", err)
			}

			x0 := int(bandRect[0])
			y0 := int(bandRect[1])
			x1 := int(bandRect[2])
			y1 := int(bandRect[3])
			const grid = 5
			var painted [][3]int
			for i := 0; i <= grid; i++ {
				for j := 0; j <= grid; j++ {
					px := x0 + (x1-x0)*i/grid
					py := y0 + (y1-y0)*j/grid
					if !inBounds(img, px, py) {
						continue
					}
					r, g, b, _ := img.At(px, py).RGBA()
					pix := [3]int{int(r >> 8), int(g >> 8), int(b >> 8)}
					if !pixelClose(pix, bgRGB, 16) {
						painted = append(painted, pix)
					}
				}
			}
			if len(painted) > 0 {
				t.Errorf("AC1-pixel: %d sample point(s) below the hamburger on %s painted a non-bg colour. bg=%v painted=%v band=[%d,%d -> %d,%d]",
					len(painted), page, bgRGB, painted, x0, y0, x1, y1)
			}
		})
	}
}
