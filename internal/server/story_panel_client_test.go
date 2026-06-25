//go:build integration

package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// story_panel_client_test.go drives the REAL project_detail template + the real
// story_panel.js / alpine.min.js in a headless browser, served from a local
// httptest page with NO /login and NO session — the codegraph_render_chromedp
// static-page pattern applied to the story panel (sty_af7d7b1a). It replaces
// the dev-login→cross-navigate chromedp tests whose session race quarantined
// them on CI: the page is the real renderProjectDetail output, so there is no
// fixture drift, and the browser never does an authenticated cross-navigate, so
// the race cannot occur. CI is unit-only (these need a browser); the markup
// contract these bind to is pinned browser-free in story_panel_render_test.go.

// skipClientJSInCI quarantines a browser test on a GH runner (CI=true) — NOT a
// session-race quarantine (these carry no dev-login→navigate), but the simple
// "headless Chrome is unavailable in the unit-only CI tier" gate the
// codegraph_render precedent uses. The behaviour runs locally / on a capable
// runner; the server-render contract runs in CI.
func skipClientJSInCI(t *testing.T) {
	t.Helper()
	if os.Getenv("CI") != "" {
		t.Skip("story panel client-JS needs a browser; CI is unit-only (sty_af7d7b1a)")
	}
}

// browserCtx boots a headless Chrome with a 60s deadline.
func browserCtx(t *testing.T) context.Context {
	t.Helper()
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(),
		append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.Flag("headless", true),
			chromedp.Flag("disable-gpu", true),
			chromedp.Flag("no-sandbox", true),
			chromedp.Flag("disable-dev-shm-usage", true),
		)...,
	)
	t.Cleanup(cancelAlloc)
	browser, cancelBrowser := chromedp.NewContext(allocCtx)
	t.Cleanup(cancelBrowser)
	runCtx, cancelRun := context.WithTimeout(browser, 60*time.Second)
	t.Cleanup(cancelRun)
	return runCtx
}

// servePanel renders data through the real template and serves it on a local
// httptest mux: "/" returns the page for ANY query (so applyToServer's
// ?stories_q= navigation re-renders + reseeds the chip from the URL, exactly
// like the live server but with no auth), "/static/" serves the on-disk assets
// (the real story_panel.js + alpine.min.js), and extra routes register stubs
// (e.g. the ledger fragment). Returns the server URL.
func servePanel(t *testing.T, data projectDetailData, routes map[string]http.HandlerFunc) string {
	t.Helper()
	page := renderProjectDetail(t, data)
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	for pattern, h := range routes {
		mux.HandleFunc(pattern, h)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(page))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

// waitPanelReady waits until Alpine has bound storyPanel and rendered the
// default chip strip — the canary that proves the component mounted (the
// script-order regression the live test guarded against). No auth, no nav race.
func waitPanelReady(t *testing.T, ctx context.Context, url string) {
	t.Helper()
	if err := chromedp.Run(ctx,
		chromedp.Navigate(url),
		chromedp.WaitVisible(`[data-field="panel-stories-chip-status-open"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("panel never mounted at %s: %v", url, err)
	}
}

func setSearch(t *testing.T, ctx context.Context, value string) {
	t.Helper()
	js := fmt.Sprintf(`(() => {
		const el = document.querySelector('[data-field="panel-stories-search"]');
		if (!el) { return false; }
		el.value = %q;
		el.dispatchEvent(new Event('input', { bubbles: true }));
		return true;
	})()`, value)
	var ok bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &ok)); err != nil || !ok {
		t.Fatalf("set search %q: ok=%v err=%v", value, ok, err)
	}
}

// visibleTitles returns the set of story-row data-title whose row is visible
// (offsetParent != null, i.e. not display:none'd by x-show).
func visibleTitles(t *testing.T, ctx context.Context) map[string]bool {
	t.Helper()
	var got []string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`
		Array.from(document.querySelectorAll('tr.story-row'))
			.filter(r => r.offsetParent !== null)
			.map(r => r.dataset.title || '')`, &got)); err != nil {
		t.Fatalf("read visible titles: %v", err)
	}
	out := make(map[string]bool, len(got))
	for _, s := range got {
		out[s] = true
	}
	return out
}

// orderedVisible returns visible story-row titles in DOM order (post-reorder).
func orderedVisible(t *testing.T, ctx context.Context) []string {
	t.Helper()
	var got []string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`
		Array.from(document.querySelectorAll('tr.story-row'))
			.filter(r => r.offsetParent !== null)
			.map(r => r.dataset.title || '')`, &got)); err != nil {
		t.Fatalf("read ordered titles: %v", err)
	}
	return got
}

func evalString(t *testing.T, ctx context.Context, js string) string {
	t.Helper()
	var out string
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &out)); err != nil {
		t.Fatalf("eval %s: %v", js, err)
	}
	return out
}

// row builds a storyRow with the fields the panel reads.
func row(id, title, status, priority, category string, tags ...string) storyRow {
	return storyRow{
		ID: id, Title: title, Status: status, StatusRank: 1,
		Priority: priority, Category: category, Tags: tags,
		UpdatedAt: time.Date(2026, 5, 27, 16, 0, 0, 0, time.UTC),
	}
}

func panelData(stories ...storyRow) projectDetailData {
	return projectDetailData{
		Project:       projectRow{ID: "proj_client", Name: "client-panel"},
		StoryFiltered: len(stories),
		StoryTotal:    len(stories),
		EngOrangeSecs: 300,
		EngRedSecs:    900,
		Stories:       stories,
	}
}

// TestStoryPanelClient_FilterVisibility covers matchesRow via x-show: the
// default status:open chip hides done/cancelled rows, a tags: filter narrows to
// matching rows, and clear-all restores the default visible set. (Replaces the
// AC3–AC6 visibility assertions of TestProjectDetailPanel_Chromedp.)
func TestStoryPanelClient_FilterVisibility(t *testing.T) {
	skipClientJSInCI(t)
	url := servePanel(t, panelData(
		row("sty_a", "alpha", "backlog", "medium", "feature", "area:portal", "epic:test"),
		row("sty_b", "beta", "backlog", "high", "bug", "area:other"),
		row("sty_g", "gamma", "ready", "low", "feature", "area:portal"),
		row("sty_d", "delta", "done", "medium", "chore", "area:other"),
	), nil)
	ctx := browserCtx(t)
	waitPanelReady(t, ctx, url)

	// Default status:open → done row hidden, the rest visible.
	if vis := visibleTitles(t, ctx); !vis["alpha"] || !vis["beta"] || !vis["gamma"] || vis["delta"] {
		t.Errorf("default filter visibility wrong: %v (want alpha/beta/gamma, not delta)", vis)
	}

	// tags:area:portal → only the two area:portal rows.
	setSearch(t, ctx, "tags:area:portal")
	if err := chromedp.Run(ctx, chromedp.Sleep(150*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if vis := visibleTitles(t, ctx); !vis["alpha"] || !vis["gamma"] || vis["beta"] || vis["delta"] {
		t.Errorf("tags:area:portal visibility wrong: %v (want alpha/gamma only)", vis)
	}

	// Clear → back to defaults (delta still hidden by status:open).
	setSearch(t, ctx, "")
	if err := chromedp.Run(ctx, chromedp.Sleep(150*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if vis := visibleTitles(t, ctx); !vis["beta"] || vis["delta"] {
		t.Errorf("post-clear visibility wrong: %v (want beta back, delta hidden)", vis)
	}
}

// TestStoryPanelClient_TagOrAndChipRemoval covers the sty_f4a72a6e tag-OR + chip
// removal: two tags: tokens union, dropping one chip narrows, and the pure
// removeFromQuery rewrite via window.storyPanelFactory.__test__.
func TestStoryPanelClient_TagOrAndChipRemoval(t *testing.T) {
	skipClientJSInCI(t)
	url := servePanel(t, panelData(
		row("sty_a", "alpha changelog", "backlog", "medium", "feature", "epic:changelog"),
		row("sty_b", "beta changelog", "backlog", "medium", "feature", "epic:changelog"),
		row("sty_g", "gamma site", "backlog", "medium", "feature", "epic:site-content"),
		row("sty_d", "delta site", "backlog", "medium", "feature", "epic:site-content"),
	), nil)
	ctx := browserCtx(t)
	waitPanelReady(t, ctx, url)

	// Two tag tokens → union of all four.
	setSearch(t, ctx, "tags:epic:changelog tags:epic:site-content")
	if err := chromedp.Run(ctx,
		chromedp.WaitVisible(`[data-field="panel-stories-chip-tags-epic:changelog"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-field="panel-stories-chip-tags-epic:site-content"]`, chromedp.ByQuery),
		chromedp.Sleep(150*time.Millisecond),
	); err != nil {
		t.Fatalf("both tag chips: %v", err)
	}
	if vis := visibleTitles(t, ctx); !(vis["alpha changelog"] && vis["beta changelog"] && vis["gamma site"] && vis["delta site"]) {
		t.Errorf("tag-OR union wrong: %v (want all four)", vis)
	}

	// Pure-logic removeFromQuery via the __test__ accessor (same code path the
	// chip-x handler uses).
	var rewrites struct {
		DropOneTag       string `json:"drop_one_tag"`
		DropOrder        string `json:"drop_order"`
		DropOrderUnknown string `json:"drop_order_unknown"`
		DropSearch       string `json:"drop_search"`
		DropOneStatus    string `json:"drop_one_status"`
	}
	if err := chromedp.Run(ctx, chromedp.Evaluate(`(() => {
		const r = window.storyPanelFactory.__test__.removeFromQuery;
		return {
			drop_one_tag:       r("tags:epic:changelog tags:epic:other order:title", "tags", "epic:changelog"),
			drop_order:         r("tags:area:portal order:title", "order", "title"),
			drop_order_unknown: r("order:order", "order", "order"),
			drop_search:        r("tags:area:portal free text here", "search", ""),
			drop_one_status:    r("status:ready,review priority:high", "status", "ready"),
		};
	})()`, &rewrites)); err != nil {
		t.Fatalf("removeFromQuery eval: %v", err)
	}
	if rewrites.DropOneTag != "tags:epic:other order:title" {
		t.Errorf("drop_one_tag: got %q", rewrites.DropOneTag)
	}
	if rewrites.DropOrder != "tags:area:portal" {
		t.Errorf("drop_order: got %q", rewrites.DropOrder)
	}
	if rewrites.DropOrderUnknown != "" {
		t.Errorf("drop_order_unknown: got %q want empty", rewrites.DropOrderUnknown)
	}
	if rewrites.DropSearch != "tags:area:portal" {
		t.Errorf("drop_search: got %q", rewrites.DropSearch)
	}
	if rewrites.DropOneStatus != "status:review priority:high" {
		t.Errorf("drop_one_status: got %q", rewrites.DropOneStatus)
	}

	// Click the changelog chip's x → removeChip → applyToServer navigates to the
	// narrowed ?stories_q=; after re-render only site rows match.
	if err := chromedp.Run(ctx, chromedp.Evaluate(`(() => {
		const chip = document.querySelector('[data-field="panel-stories-chip-tags-epic:changelog"]');
		const btn = chip && chip.querySelector('.panel-filter-chip-remove');
		if (btn) { btn.click(); return true; }
		return false;
	})()`, nil)); err != nil {
		t.Fatalf("click chip-x: %v", err)
	}
	// Navigation + re-mount; wait for the site-content chip to seed from the URL.
	if err := chromedp.Run(ctx,
		chromedp.WaitVisible(`[data-field="panel-stories-chip-tags-epic:site-content"]`, chromedp.ByQuery),
		chromedp.Sleep(150*time.Millisecond),
	); err != nil {
		t.Fatalf("post-removal re-mount: %v", err)
	}
	if vis := visibleTitles(t, ctx); !(vis["gamma site"] && vis["delta site"]) || vis["alpha changelog"] || vis["beta changelog"] {
		t.Errorf("after chip-x, visibility wrong: %v (want site rows only)", vis)
	}
}

// TestStoryPanelClient_Order covers applyStoryOrder + restoreStoryOrder: order
// tokens physically reorder the rows, epic-order sorts numerically and sinks
// missing tags, an alphanumeric prefix (order:order) sorts naturally, an unknown
// field still renders an order chip (never a search chip), and removing the
// order token restores server order.
func TestStoryPanelClient_Order(t *testing.T) {
	skipClientJSInCI(t)
	// Server (DOM) order = the seed slice order. Tags carry epic-order:<n> (one
	// missing → sinks) and order:<alnum> (10/2/a/b → natural-sort discriminator).
	url := servePanel(t, panelData(
		row("sty_a", "alpha", "backlog", "medium", "feature", "epic-order:2", "order:10"),
		row("sty_g", "gamma", "backlog", "medium", "feature", "order:a"),
		row("sty_b", "beta", "backlog", "medium", "feature", "epic-order:1", "order:2"),
	), nil)
	ctx := browserCtx(t)
	waitPanelReady(t, ctx, url)

	// Default DOM order = seed order.
	if got := orderedVisible(t, ctx); !reflect.DeepEqual(got, []string{"alpha", "gamma", "beta"}) {
		t.Errorf("default order: got %v want [alpha gamma beta]", got)
	}

	// order:title → ascending alphabetical.
	setSearch(t, ctx, "order:title")
	if err := chromedp.Run(ctx,
		chromedp.WaitVisible(`[data-field="panel-stories-chip-order-title"]`, chromedp.ByQuery),
		chromedp.Sleep(150*time.Millisecond)); err != nil {
		t.Fatalf("order:title chip: %v", err)
	}
	if got := orderedVisible(t, ctx); !reflect.DeepEqual(got, []string{"alpha", "beta", "gamma"}) {
		t.Errorf("order:title: got %v want [alpha beta gamma]", got)
	}

	// Remove the order token → restoreStoryOrder puts rows back to server order.
	setSearch(t, ctx, "")
	if err := chromedp.Run(ctx, chromedp.Sleep(150*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if got := orderedVisible(t, ctx); !reflect.DeepEqual(got, []string{"alpha", "gamma", "beta"}) {
		t.Errorf("restore after clearing order: got %v want [alpha gamma beta]", got)
	}

	// order:epic-order → numeric tag sort (beta=1, alpha=2), gamma (no tag) sinks.
	setSearch(t, ctx, "order:epic-order")
	if err := chromedp.Run(ctx,
		chromedp.WaitVisible(`[data-field="panel-stories-chip-order-epic-order"]`, chromedp.ByQuery),
		chromedp.Sleep(150*time.Millisecond)); err != nil {
		t.Fatalf("order:epic-order chip: %v", err)
	}
	if got := orderedVisible(t, ctx); !reflect.DeepEqual(got, []string{"beta", "alpha", "gamma"}) {
		t.Errorf("order:epic-order: got %v want [beta alpha gamma]", got)
	}

	// order:order → natural sort of the order:<alnum> tag (2 < 10 < a).
	setSearch(t, ctx, "order:order")
	if err := chromedp.Run(ctx,
		chromedp.WaitVisible(`[data-field="panel-stories-chip-order-order"]`, chromedp.ByQuery),
		chromedp.Sleep(150*time.Millisecond)); err != nil {
		t.Fatalf("order:order chip: %v", err)
	}
	if got := orderedVisible(t, ctx); !reflect.DeepEqual(got, []string{"beta", "alpha", "gamma"}) {
		// beta=order:2, alpha=order:10, gamma=order:a → 2 < 10 < a
		t.Errorf("order:order natural sort: got %v want [beta alpha gamma]", got)
	}

	// order:bogus → still an order chip, never a search chip (no fallthrough).
	setSearch(t, ctx, "order:bogus")
	if err := chromedp.Run(ctx,
		chromedp.WaitVisible(`[data-field="panel-stories-chip-order-bogus"]`, chromedp.ByQuery)); err != nil {
		t.Fatalf("order:bogus chip never rendered: %v", err)
	}
	var searchChip bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		`!!document.querySelector('[data-field="panel-stories-chip-search-order:bogus"]')`, &searchChip)); err != nil {
		t.Fatal(err)
	}
	if searchChip {
		t.Error("order:bogus fell through to a search chip")
	}
}

// TestStoryPanelClient_FilterURL covers stories_q URL persistence: a deep-link
// applies the filter on load, typing mirrors to the URL, clearing drops the
// param, and reload restores the chip. (Replaces TestStoryFilterURL.)
func TestStoryPanelClient_FilterURL(t *testing.T) {
	skipClientJSInCI(t)
	url := servePanel(t, panelData(
		row("sty_a", "alpha", "backlog", "medium", "feature", "area:portal"),
		row("sty_b", "beta", "backlog", "medium", "feature", "area:other"),
		row("sty_g", "gamma", "ready", "medium", "feature", "area:portal"),
	), nil)
	ctx := browserCtx(t)

	// Deep-link applies the filter without interaction.
	if err := chromedp.Run(ctx,
		chromedp.Navigate(url+"?stories_q=tags%3Aarea%3Aportal"),
		chromedp.WaitVisible(`[data-field="panel-stories-chip-tags-area:portal"]`, chromedp.ByQuery),
		chromedp.Sleep(150*time.Millisecond),
	); err != nil {
		t.Fatalf("deep-link: %v", err)
	}
	if got := evalString(t, ctx, `document.querySelector('[data-field="panel-stories-search"]').value`); got != "tags:area:portal" {
		t.Errorf("deep-link search input: got %q", got)
	}
	if vis := visibleTitles(t, ctx); vis["beta"] {
		t.Error("deep-link filter not applied — beta (area:other) visible")
	}

	// Typing mirrors to the URL.
	setSearch(t, ctx, "tags:area:other")
	if err := chromedp.Run(ctx, chromedp.Sleep(100*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	loc := evalString(t, ctx, `window.location.search`)
	if loc != "?stories_q=tags%3Aarea%3Aother" && loc != "?stories_q=tags:area:other" {
		t.Errorf("URL not updated after typing: %q", loc)
	}

	// Clearing drops the param.
	setSearch(t, ctx, "")
	if err := chromedp.Run(ctx, chromedp.Sleep(100*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if loc := evalString(t, ctx, `window.location.search`); loc != "" {
		t.Errorf("stories_q not removed after clear: %q", loc)
	}

	// Reload restores from a seeded URL.
	if err := chromedp.Run(ctx,
		chromedp.Navigate(url+"?stories_q=tags%3Aarea%3Aportal"),
		chromedp.WaitVisible(`[data-field="panel-stories-chip-tags-area:portal"]`, chromedp.ByQuery),
		chromedp.Reload(),
		chromedp.WaitVisible(`[data-field="panel-stories-chip-tags-area:portal"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("reload restore: %v", err)
	}
	if got := evalString(t, ctx, `document.querySelector('[data-field="panel-stories-search"]').value`); got != "tags:area:portal" {
		t.Errorf("search input after reload: got %q", got)
	}
}

// TestStoryPanelClient_EngagementDotAging drives window.satAgeEngagementDots
// with a fixed now over rows seeded with fixed timestamps: fresh→plain,
// past-orange→is-orange, past-red→is-red, expired-lease→is-stale (dormant but
// visible), the tooltip text, and row-height invariance. Deterministic — no
// reload, no wall-clock. (Replaces TestEngagementDot_Chromedp's aging half; the
// status qualification is covered by engagements_test.go + the spinner emit by
// story_panel_render_test.go.)
func TestStoryPanelClient_EngagementDotAging(t *testing.T) {
	skipClientJSInCI(t)
	const base = "2026-01-01T00:00:00Z"    // last_seen for the aging rows
	const lease = "2026-01-01T02:00:00Z"   // far-future lease (not stale)
	const expired = "2025-12-31T23:59:00Z" // past lease → stale

	data := panelData(
		func() storyRow {
			r := row("sty_fresh", "fresh", "in_progress", "medium", "feature")
			r.EngLastSeen = base
			r.EngLeaseUntil = lease
			return r
		}(),
		func() storyRow {
			r := row("sty_stale", "stale", "in_progress", "medium", "feature")
			r.EngLastSeen = base
			r.EngLeaseUntil = expired
			return r
		}(),
		row("sty_none", "none", "in_progress", "medium", "feature"),
	)
	url := servePanel(t, data, nil)
	ctx := browserCtx(t)
	waitPanelReady(t, ctx, url)

	spinnerClass := func(id string) string {
		return evalString(t, ctx, fmt.Sprintf(
			`(function(){var d=document.querySelector('tr[data-id=%q] .activity-spinner');return d?d.className:'<missing>'})()`, id))
	}
	age := func(iso string) {
		if err := chromedp.Run(ctx, chromedp.Evaluate(
			fmt.Sprintf(`window.satAgeEngagementDots(Date.parse(%q))`, iso), nil)); err != nil {
			t.Fatalf("age %s: %v", iso, err)
		}
	}

	// now = base → fresh is plain (no color), within thresholds.
	age("2026-01-01T00:00:00Z")
	if cls := spinnerClass("sty_fresh"); cls != "activity-spinner" {
		t.Errorf("fresh at t=0 should be plain, got %q", cls)
	}
	// now = base+6m (> 300s orange) → is-orange.
	age("2026-01-01T00:06:00Z")
	if cls := spinnerClass("sty_fresh"); !strings.Contains(cls, "is-orange") {
		t.Errorf("fresh at +6m should be is-orange, got %q", cls)
	}
	// now = base+16m (> 900s red) → is-red.
	age("2026-01-01T00:16:00Z")
	if cls := spinnerClass("sty_fresh"); !strings.Contains(cls, "is-red") {
		t.Errorf("fresh at +16m should be is-red, got %q", cls)
	}
	// Expired lease → is-stale regardless of age, and still visible (dormant).
	if cls := spinnerClass("sty_stale"); !strings.Contains(cls, "is-stale") {
		t.Errorf("expired-lease spinner should be is-stale, got %q", cls)
	}
	var staleVisible bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		`(function(){var d=document.querySelector('tr[data-id="sty_stale"] .activity-spinner');return !!(d && d.offsetParent !== null)})()`, &staleVisible)); err != nil {
		t.Fatal(err)
	}
	if !staleVisible {
		t.Error("stale (lapsed-lease) spinner must remain visible (dormant), not hidden")
	}
	// Tooltip honesty: "last activity Xm ago".
	if got := evalString(t, ctx, `document.querySelector('tr[data-id="sty_fresh"] .activity-spinner').title`); !strings.HasPrefix(got, "last activity ") || !strings.HasSuffix(got, "m ago") {
		t.Errorf("tooltip = %q, want 'last activity Xm ago'", got)
	}
	// Un-engaged row carries no spinner.
	if cls := spinnerClass("sty_none"); cls != "<missing>" {
		t.Errorf("un-engaged row must render no spinner, got %q", cls)
	}
	// Row height unchanged with vs without the spinner.
	var heights []float64
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		`(function(){var r=document.querySelector('tr[data-id="sty_fresh"]');var b=r.offsetHeight;var d=r.querySelector('.activity-spinner');var p=d.parentNode;p.removeChild(d);var a=r.offsetHeight;p.appendChild(d);return [b,a]})()`, &heights)); err != nil {
		t.Fatal(err)
	}
	if len(heights) != 2 || heights[0] != heights[1] {
		t.Errorf("spinner changed row height: %v", heights)
	}
}

// TestStoryPanelClient_LedgerLazyLoad covers the inline tab panel: expanding a
// row shows the Description tab, clicking Ledger lazy-loads the trace fragment
// via window.loadStoryFragment (stubbed in the mux) and swaps it into the ledger
// container, and switching back to Description toggles detailTab. (Replaces
// TestProjectDetailStoryTabs_Chromedp.)
func TestStoryPanelClient_LedgerLazyLoad(t *testing.T) {
	skipClientJSInCI(t)
	r := row("sty_x", "tabbed story", "in_progress", "medium", "feature")
	r.BodyHTML = "<p>the description body</p>"
	url := servePanel(t, panelData(r), map[string]http.HandlerFunc{
		"/stories/sty_x/trace.fragment": func(w http.ResponseWriter, req *http.Request) {
			_, _ = w.Write([]byte(`<div data-field="ledger-loaded">LEDGER ROWS HERE</div>`))
		},
	})
	ctx := browserCtx(t)
	waitPanelReady(t, ctx, url)

	// Expand the row (click it) → detail row visible, Description tab active.
	if err := chromedp.Run(ctx,
		chromedp.Click(`tr[data-id="sty_x"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`tr.story-detail[data-detail-for="sty_x"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("expand row: %v", err)
	}

	// Click the Ledger tab → openLedger → loadStoryFragment fetches the stub.
	if err := chromedp.Run(ctx,
		chromedp.Click(`tr.story-detail[data-detail-for="sty_x"] [data-tab="ledger"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-field="ledger-loaded"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("ledger lazy-load: %v", err)
	}
	if got := evalString(t, ctx, `document.querySelector('tr.story-detail[data-detail-for="sty_x"] [data-field="story-ledger"]').textContent`); !strings.Contains(got, "LEDGER ROWS HERE") {
		t.Errorf("ledger fragment not swapped in: %q", got)
	}

	// Switch back to Description.
	if err := chromedp.Run(ctx,
		chromedp.Click(`tr.story-detail[data-detail-for="sty_x"] [data-tab="description"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`tr.story-detail[data-detail-for="sty_x"] [data-field="story-description-body"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("switch back to description: %v", err)
	}
}

// TestStoryPanelClient_DocumentsLazyLoad covers the Documents tab (sty_bf2fc8e1):
// clicking it lazy-loads the attached-documents fragment via
// window.loadStoryDocsFragment (stubbed in the mux) and swaps it into the
// documents container — peer to the ledger lazy-load.
func TestStoryPanelClient_DocumentsLazyLoad(t *testing.T) {
	skipClientJSInCI(t)
	r := row("sty_x", "tabbed story", "in_progress", "medium", "feature")
	r.BodyHTML = "<p>the description body</p>"
	url := servePanel(t, panelData(r), map[string]http.HandlerFunc{
		"/stories/sty_x/documents.fragment": func(w http.ResponseWriter, req *http.Request) {
			_, _ = w.Write([]byte(`<div data-field="docs-loaded">DOC LIST HERE</div>`))
		},
	})
	ctx := browserCtx(t)
	waitPanelReady(t, ctx, url)

	if err := chromedp.Run(ctx,
		chromedp.Click(`tr[data-id="sty_x"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`tr.story-detail[data-detail-for="sty_x"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("expand row: %v", err)
	}

	// Click the Documents tab → openDocuments → loadStoryDocsFragment fetches the stub.
	if err := chromedp.Run(ctx,
		chromedp.Click(`tr.story-detail[data-detail-for="sty_x"] [data-tab="documents"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-field="docs-loaded"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("documents lazy-load: %v", err)
	}
	if got := evalString(t, ctx, `document.querySelector('tr.story-detail[data-detail-for="sty_x"] [data-field="story-documents"]').textContent`); !strings.Contains(got, "DOC LIST HERE") {
		t.Errorf("documents fragment not swapped in: %q", got)
	}
}

// TestStoryPanelClient_CategoryChip covers the row category chip: clicking it
// runs addCategoryToQuery → applyToServer, navigating to ?stories_q=category:<v>.
// (Replaces TestStoryCategoryChip.)
func TestStoryPanelClient_CategoryChip(t *testing.T) {
	skipClientJSInCI(t)
	url := servePanel(t, panelData(
		row("sty_a", "alpha", "backlog", "medium", "improvement"),
		row("sty_b", "beta", "backlog", "medium", "bug"),
	), nil)
	ctx := browserCtx(t)
	waitPanelReady(t, ctx, url)

	// Click alpha's category chip → navigates to ?stories_q=category:improvement.
	if err := chromedp.Run(ctx, chromedp.Evaluate(`(() => {
		const btn = Array.from(document.querySelectorAll('button.category-chip'))
			.find(b => b.dataset && b.dataset.category === 'improvement');
		if (btn) { btn.click(); return true; }
		return false;
	})()`, nil)); err != nil {
		t.Fatalf("click category chip: %v", err)
	}
	if err := chromedp.Run(ctx,
		chromedp.WaitVisible(`[data-field="panel-stories-chip-category-improvement"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("category chip never seeded from URL after navigation: %v", err)
	}
	if loc := evalString(t, ctx, `window.location.search`); !strings.Contains(loc, "category") || !strings.Contains(loc, "improvement") {
		t.Errorf("URL after category click: %q want category:improvement", loc)
	}
}
