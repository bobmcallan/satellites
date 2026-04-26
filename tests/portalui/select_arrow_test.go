//go:build portalui

package portalui

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"net/url"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestSelectArrow_DarkThemeNoTile covers story_e5efcb6d: on dark theme,
// <select> dropdown carets must render as a single arrow on the right
// edge — not as a tiled fill across the control. The bug was a CSS
// shorthand reset (`background: #111`) on the dark-theme select rule
// wiping out the inherited `background-repeat: no-repeat`.
//
// Two assertions:
//
//  1. getComputedStyle(select).backgroundRepeat === 'no-repeat'
//  2. screenshot pixel at the centre of the select control matches
//     the form bg colour #111 within tolerance — NOT the arrow grey
//     #e0e0e0
func TestSelectArrow_DarkThemeNoTile(t *testing.T) {
	h := StartHarness(t)

	parent, cancel := withTimeout(context.Background(), browserDeadline)
	defer cancel()
	browserCtx, cancelBrowser := newChromedpContext(t, parent)
	defer cancelBrowser()

	if err := installSessionCookie(browserCtx, h); err != nil {
		t.Fatalf("install session cookie: %v", err)
	}

	// Force dark theme so the dark-theme rules apply (the harness's
	// default theme cookie is unset; without this the page resolves
	// to "dark" via the head.html script's default, but cookie-set is
	// the deterministic path).
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

	const probe = `(() => {
		const sel = document.querySelector('.tasks-filters select');
		if (!sel) { return {error: 'select not found'}; }
		const cs = getComputedStyle(sel);
		const r = sel.getBoundingClientRect();
		return {
			backgroundRepeat: cs.backgroundRepeat,
			backgroundImage: cs.backgroundImage.slice(0, 80),
			rect: [r.left, r.top, r.width, r.height],
		};
	})()`

	var probeResult struct {
		BackgroundRepeat string    `json:"backgroundRepeat"`
		BackgroundImage  string    `json:"backgroundImage"`
		Rect             []float64 `json:"rect"`
		Error            string    `json:"error"`
	}
	var screenshot []byte
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(h.BaseURL+"/tasks"),
		chromedp.WaitVisible(`.tasks-filters select`, chromedp.ByQuery),
		chromedp.Sleep(400*time.Millisecond),
		chromedp.Evaluate(probe, &probeResult),
		chromedp.FullScreenshot(&screenshot, 100),
	); err != nil {
		t.Fatalf("navigate /tasks dark: %v", err)
	}
	if probeResult.Error != "" {
		t.Fatalf("probe: %s", probeResult.Error)
	}

	// AC1: computed background-repeat must be no-repeat.
	if probeResult.BackgroundRepeat != "no-repeat" {
		t.Errorf("AC1: select background-repeat = %q, want 'no-repeat' — caret arrow is tiling. background-image=%q rect=%v",
			probeResult.BackgroundRepeat, probeResult.BackgroundImage, probeResult.Rect)
	}

	// AC2: pixel sample at the centre of the select must match the
	// form bg colour #111 within tolerance, NOT the arrow grey
	// #e0e0e0.
	if len(probeResult.Rect) != 4 {
		t.Fatalf("AC2: rect not populated: %v", probeResult.Rect)
	}
	cx := int(probeResult.Rect[0] + probeResult.Rect[2]/2)
	cy := int(probeResult.Rect[1] + probeResult.Rect[3]/2)

	img, err := png.Decode(bytes.NewReader(screenshot))
	if err != nil {
		t.Fatalf("decode screenshot: %v", err)
	}
	if !inBounds(img, cx, cy) {
		t.Fatalf("AC2: sample point (%d,%d) out of viewport bounds %v", cx, cy, img.Bounds())
	}
	r, g, b, _ := img.At(cx, cy).RGBA()
	pix := [3]int{int(r >> 8), int(g >> 8), int(b >> 8)}

	const formBgR, formBgG, formBgB = 17, 17, 17                // #111
	const arrowR, arrowG, arrowB = 0xe0, 0xe0, 0xe0             // #e0e0e0
	if !pixelClose(pix, [3]int{formBgR, formBgG, formBgB}, 16) {
		t.Errorf("AC2: pixel at centre of select (%d,%d) = %v, want close to #111 (form bg). If close to #%02x%02x%02x the caret is tiling. background-repeat=%q",
			cx, cy, pix, arrowR, arrowG, arrowB, probeResult.BackgroundRepeat)
	}
	_ = fmt.Sprintf("%d", arrowR) // keep arrow constants referenced for the error message
}
