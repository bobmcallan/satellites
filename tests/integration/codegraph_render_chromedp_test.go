//go:build integration

package integration_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// codegraphPage is a standalone page carrying a B1 language-codegraph block plus the
// real codegraph-init.js + styles.css (served from the on-disk static dir). It
// exercises the B2 viewer's JS render path WITHOUT the portal — no auth, no DB, no
// session (the portal chromedp harness has a known session race). The JSON has no
// <,>,& so it is safe as code-block text content.
const codegraphPage = `<!doctype html><html lang="en"><head>
<link rel="stylesheet" href="/static/styles.css">
<script defer src="/static/codegraph-init.js"></script>
</head><body>
<section data-section="project-codegraph"><h2>codegraph</h2>
<div class="changelog-content">
<pre><code class="language-codegraph">{"graph":{"directed":true,"label":"example.com/m","nodes":{"example.com/m/cli":{"label":"cli","metadata":{"package":"cli"}},"example.com/m/index":{"label":"index","metadata":{"package":"index"}}},"edges":[{"source":"example.com/m/cli","target":"example.com/m/index","relation":"depends-on"}]}}</code></pre>
</div></section>
</body></html>`

// emptyPage carries the same scripts but NO codegraph block — codegraph-init must
// load nothing (AC3: lazy).
const emptyPage = `<!doctype html><html lang="en"><head>
<script defer src="/static/codegraph-init.js"></script>
</head><body><p data-field="empty">no graph here</p></body></html>`

// TestCodegraphRender_Chromedp proves codegraph-init.js renders the embedded
// language-codegraph block as an interactive cytoscape graph (AC1), lazy-loading the
// bundle only when a block is present (AC3) — driven on a standalone page so the
// portal session race cannot flake it.
func TestCodegraphRender_Chromedp(t *testing.T) {
	skipInCI(t, "codegraph render chromedp needs a browser; CI is unit-only")

	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("../../internal/server/static"))))
	mux.HandleFunc("/graph", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(codegraphPage)) })
	mux.HandleFunc("/empty", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(emptyPage)) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	bctx := newBrowserCtx(t)

	// AC1: the block renders to an interactive cytoscape graph (canvas + filter), and
	// the bundle is fetched.
	var canvasCount int
	var bundleFetched, hasFilter bool
	if err := chromedp.Run(bctx,
		chromedp.Navigate(srv.URL+"/graph"),
		chromedp.WaitVisible(`.codegraph[data-codegraph-rendered="1"]`, chromedp.ByQuery),
		chromedp.Poll(`document.querySelectorAll('[data-field="codegraph-canvas"] canvas').length`, &canvasCount,
			chromedp.WithPollingTimeout(8*time.Second)),
		chromedp.Evaluate(`!!document.querySelector('script[src="/static/cytoscape.min.js"]')`, &bundleFetched),
		chromedp.Evaluate(`!!document.querySelector('[data-field="codegraph-filter"]')`, &hasFilter),
	); err != nil {
		t.Fatalf("AC1: codegraph viewer did not render: %v", err)
	}
	if canvasCount < 1 {
		t.Errorf("AC1: no cytoscape <canvas> rendered (count=%d)", canvasCount)
	}
	if !bundleFetched {
		t.Errorf("AC1: cytoscape bundle not fetched on a page with a codegraph block")
	}
	if !hasFilter {
		t.Errorf("AC1: filter control missing (pan/zoom are cytoscape built-ins)")
	}

	// AC3: a page with no block never fetches the bundle.
	var bundleOnEmpty bool
	if err := chromedp.Run(bctx,
		chromedp.Navigate(srv.URL+"/empty"),
		chromedp.WaitVisible(`[data-field="empty"]`, chromedp.ByQuery),
		chromedp.Sleep(time.Second),
		chromedp.Evaluate(`!!document.querySelector('script[src="/static/cytoscape.min.js"]')`, &bundleOnEmpty),
	); err != nil {
		t.Fatalf("AC3: empty page: %v", err)
	}
	if bundleOnEmpty {
		t.Errorf("AC3: cytoscape bundle fetched on a page with no codegraph block (not lazy)")
	}
}
