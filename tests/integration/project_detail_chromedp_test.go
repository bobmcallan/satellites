//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/chromedp"
)

// TestProjectDetailPanel_Chromedp drives a headless browser to verify the
// story panel's *rendered* DOM matches the design intent — not just the
// server-rendered HTML markup. Catches the regression class where the
// HTML-string tests pass but Alpine fails to bind on the live page
// (sty_e4266b16; symptom captured in sty_2f429877).
//
// Seeds four stories with mixed status/tags so the default status:open
// chip's exclude-done/cancelled behaviour is observable, and so the
// `tags:area:portal` filter has both a hit and a miss to discriminate.
func TestProjectDetailPanel_Chromedp(t *testing.T) {
	env := testbootstrap.SetUpWithServer(t)

	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	docStore := document.New(env.DB)
	ledStore := ledger.New(env.DB)
	verb.SetAuthStore(env.Store)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	verb.SetDocumentStore(docStore)
	verb.SetLedgerStore(ledStore)
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
		verb.SetDocumentStore(nil)
		verb.SetLedgerStore(nil)
	})

	ctx := context.Background()
	now := time.Date(2026, 5, 25, 16, 0, 0, 0, time.UTC)
	ws, err := wsStore.Create(ctx, "", "chromedp-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{
		WorkspaceID: ws.ID,
		Name:        "chromedp-project",
	}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	type seedStory struct {
		Name     string
		Status   string
		Priority string
		Tags     []string
	}
	seeds := []seedStory{
		{"panel story alpha", "backlog", "medium", []string{"area:portal", "epic:test"}},
		{"panel story beta", "backlog", "high", []string{"area:other"}},
		{"panel story gamma", "ready", "low", []string{"area:portal"}},
		{"panel story delta", "done", "medium", []string{"area:other"}},
	}
	for _, s := range seeds {
		st, pr := s.Status, s.Priority
		tg := append([]string(nil), s.Tags...)
		req, _ := json.Marshal(verb.DocumentUpsertRequest{
			Type:      "story",
			ProjectID: pj.ID,
			Name:      s.Name,
			Status:    &st,
			Priority:  &pr,
			Tags:      &tg,
		})
		if _, err := verb.Dispatch(ctx, "document_upsert", req); err != nil {
			t.Fatalf("create %s: %v", s.Name, err)
		}
	}

	// 60s deadline — the panel test does more setup + waits than the
	// landing tests newBrowserCtx is sized for. Mirrors the boot flow
	// of newBrowserCtx but with a longer outer timeout.
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), chromedpHeadlessOpts()...)
	t.Cleanup(cancelAlloc)
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	t.Cleanup(cancelBrowser)
	bctx, cancelRun := context.WithTimeout(browserCtx, 60*time.Second)
	t.Cleanup(cancelRun)

	// Grant clipboard write so the col-id copy's navigator.clipboard.writeText
	// resolves under headless (no permission prompt). The copy is still driven
	// by a real CDP click below, which supplies the user activation the
	// clipboard API requires — sty_b7ba18b3.
	if err := chromedp.Run(bctx, browser.GrantPermissions(
		[]browser.PermissionType{browser.PermissionTypeClipboardSanitizedWrite, browser.PermissionTypeClipboardReadWrite},
	)); err != nil {
		t.Fatalf("grant clipboard permission: %v", err)
	}

	// Login + navigate to the project detail page.
	if err := chromedp.Run(bctx,
		chromedp.Navigate(env.ServerURL+"/login"),
		chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
		chromedp.Click(`button[data-action="dev-login-admin"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
		chromedp.Navigate(env.ServerURL+"/projects/"+pj.ID),
		chromedp.WaitVisible(`[data-field="stories-table"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("nav: %v", err)
	}

	// sty_589bb623: the projects page uses the global responsive margin ladder
	// (no fixed max-width cap). On a standard-width viewport the content sits
	// ~5% from each browser edge — assert paddingLeft ≈ 5% of the viewport and
	// the cap is gone. (Guards against a regression to the old 1280px centered
	// column.)
	var margin struct {
		MaxWidth string  `json:"maxWidth"`
		Ratio    float64 `json:"ratio"`
		VW       float64 `json:"vw"`
	}
	if err := chromedp.Run(bctx, chromedp.Evaluate(`(() => {
		const m = document.querySelector('main');
		const cs = getComputedStyle(m);
		const vw = document.documentElement.clientWidth;
		const pl = parseFloat(cs.paddingLeft) || 0;
		return { maxWidth: cs.maxWidth, ratio: vw ? pl / vw : 0, vw: vw };
	})()`, &margin)); err != nil {
		t.Fatalf("read main margin: %v", err)
	}
	if margin.MaxWidth != "none" {
		t.Errorf("projects main max-width = %q, want \"none\" (responsive ladder, no cap)", margin.MaxWidth)
	}
	// Only assert the 5% rung when the headless viewport is in the standard band
	// (769–1919px); the ladder is 1% below and 20% above.
	if margin.VW > 768 && margin.VW < 1920 {
		if margin.Ratio < 0.03 || margin.Ratio > 0.08 {
			t.Errorf("projects main side margin = %.3f of viewport (vw=%.0f), want ~0.05 (5%%)", margin.Ratio, margin.VW)
		}
	}

	// Wait for Alpine to bind x-data + run the initial x-for so the
	// default chip spans are in the DOM. This is the canary that
	// regressed under the wrong <script> order (alpine.min.js before
	// story_panel.js) — Alpine walked the DOM before the storyPanel
	// factory was registered, leaving the chip strip empty.
	if err := chromedp.Run(bctx,
		chromedp.WaitVisible(`[data-field="panel-stories-chip-status-open"]`, chromedp.ByQuery),
	); err != nil {
		// Dump enough state on failure to diagnose Alpine init issues
		// without re-running the test in inspector mode.
		var diag map[string]interface{}
		_ = chromedp.Run(bctx, chromedp.Evaluate(`(() => {
			const panel = document.querySelector('[data-field="panel-stories-body"]');
			const stack = panel && panel._x_dataStack && panel._x_dataStack[0];
			return {
				alpineLoaded: typeof window.Alpine,
				alpineVersion: (window.Alpine && window.Alpine.version) || 'unknown',
				stackExists: !!stack,
				stackKeys: stack ? Object.keys(stack).slice(0, 20) : null,
				scripts: Array.from(document.scripts).map(s => s.src)
			};
		})()`, &diag))
		t.Fatalf("default chip never rendered: %v\ndiag: %#v", err, diag)
	}

	// AC 3: all three default chips present.
	for _, sel := range []string{
		`[data-field="panel-stories-chip-status-open"]`,
		`[data-field="panel-stories-chip-priority-all"]`,
		`[data-field="panel-stories-chip-category-all"]`,
	} {
		var present bool
		if err := chromedp.Run(bctx,
			chromedp.Evaluate(`!!document.querySelector('`+sel+`')`, &present),
		); err != nil || !present {
			t.Errorf("default chip %s missing", sel)
		}
	}

	// AC 4: three non-done rows visible; done row hidden.
	visibleTitles, err := visibleRowTitles(bctx)
	if err != nil {
		t.Fatalf("read visible titles: %v", err)
	}
	wantVisible := map[string]bool{
		"panel story alpha": true,
		"panel story beta":  true,
		"panel story gamma": true,
	}
	for title := range wantVisible {
		if !visibleTitles[title] {
			t.Errorf("row %q expected visible under default chips, hidden", title)
		}
	}
	if visibleTitles["panel story delta"] {
		t.Errorf("row %q (status=done) should be hidden under default status:open chip", "panel story delta")
	}

	// sty_975afe24: counter chip reads "shown / total". 4 stories
	// total, 3 visible under the default status:open chip (delta is
	// done → hidden).
	var counterText string
	if err := chromedp.Run(bctx,
		chromedp.Text(`[data-field="stories-count-indicator"]`, &counterText, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("counter text: %v", err)
	}
	if !strings.Contains(counterText, "3 / 4") {
		t.Errorf("counter chip under default filter: got %q want contains '3 / 4'", counterText)
	}

	// AC 5: filter to tags:area:portal — narrows to two matching rows + adds
	// a user-set chip. Commit via navigation (?stories_q=) rather than a
	// client-only setStorySearch: the count indicator is server-authoritative
	// (filtered/total, correct across all pages — sty_1bd0098a / sty_47234d6e),
	// so it only moves on a committed query. The server renders exactly the
	// filtered rows; the chip seeds from the URL query on init.
	if err := chromedp.Run(bctx,
		chromedp.Navigate(env.ServerURL+"/projects/"+pj.ID+"?stories_q=tags%3Aarea%3Aportal"),
		chromedp.WaitVisible(`[data-field="panel-stories-chip-tags-area:portal"]`, chromedp.ByQuery),
		chromedp.Sleep(150*time.Millisecond),
	); err != nil {
		t.Fatalf("apply tag filter (navigation): %v", err)
	}
	// Alpine 3.15.12 batches x-show updates — the chip x-for produces
	// its element before every row's x-show effect re-runs. Sleep so
	// the per-row display state catches up before we assert.
	if err := chromedp.Run(bctx, chromedp.Sleep(150*time.Millisecond)); err != nil {
		t.Fatalf("settle: %v", err)
	}
	visibleTitles, err = visibleRowTitles(bctx)
	if err != nil {
		t.Fatalf("read filtered titles: %v", err)
	}
	if !visibleTitles["panel story alpha"] {
		t.Error("alpha (area:portal) should remain visible after tags:area:portal filter")
	}
	if !visibleTitles["panel story gamma"] {
		t.Error("gamma (area:portal) should remain visible after tags:area:portal filter")
	}
	if visibleTitles["panel story beta"] {
		t.Error("beta (area:other) should hide after tags:area:portal filter")
	}

	// sty_975afe24: after filter narrows visible rows to 2, the
	// "shown" half of the counter chip drops to 2; the "total" half
	// (project-wide count, path B) stays at 4.
	if err := chromedp.Run(bctx,
		chromedp.Text(`[data-field="stories-count-indicator"]`, &counterText, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("counter text after filter: %v", err)
	}
	if !strings.Contains(counterText, "2 / 4") {
		t.Errorf("counter chip under tags filter: got %q want contains '2 / 4'", counterText)
	}

	// AC 6: clear all returns to defaults — alpha/beta/gamma visible,
	// delta hidden again.
	if err := chromedp.Run(bctx,
		chromedp.Click(`[data-action="panel-stories-clear-all"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("clear all: %v", err)
	}
	// Wait for the user-set chip to disappear.
	if err := waitChipAbsent(bctx, `[data-field="panel-stories-chip-tags-area:portal"]`); err != nil {
		t.Fatalf("user chip never cleared: %v", err)
	}
	// Wait a moment so any queued reactive microtasks flush before we
	// snapshot the DOM — Alpine 3.15.12 batches x-show updates and the
	// chip-absence signal lands before every row's x-show effect has
	// re-run on the new query.
	if err := chromedp.Run(bctx, chromedp.Sleep(150*time.Millisecond)); err != nil {
		t.Fatalf("sleep: %v", err)
	}
	visibleTitles, err = visibleRowTitles(bctx)
	if err != nil {
		t.Fatalf("read post-clear titles: %v", err)
	}
	if !visibleTitles["panel story beta"] {
		t.Error("beta should re-appear after clear-all restored defaults")
	}
	if visibleTitles["panel story delta"] {
		t.Error("delta (done) should still be hidden after clear-all (default status:open re-applies)")
	}

	// AC 7: clicking a tag chip on a row appends tags:<value> to
	// search. Fire the click via JS rather than chromedp.Click so we
	// don't have to deal with CSS selector quoting around the colon
	// inside the attribute value — and because the chromedp click
	// retries until the parent context times out on this specific
	// CDP path.
	if err := chromedp.Run(bctx, chromedp.Evaluate(`(() => {
		const btn = Array.from(document.querySelectorAll('button.tag-chip'))
			.find(b => b.dataset && b.dataset.tag === 'area:other');
		if (!btn) { return false; }
		btn.click();
		return true;
	})()`, nil)); err != nil {
		t.Fatalf("click row tag chip: %v", err)
	}
	if err := chromedp.Run(bctx, chromedp.Sleep(100*time.Millisecond),
		chromedp.WaitVisible(`[data-field="panel-stories-chip-tags-area:other"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("user tag chip never appeared: %v", err)
	}
	var searchVal string
	if err := chromedp.Run(bctx,
		chromedp.Value(`[data-field="panel-stories-search"]`, &searchVal, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("read search input: %v", err)
	}
	if !strings.Contains(searchVal, "tags:area:other") {
		t.Errorf("search input after row-chip click: got %q, want it to contain tags:area:other", searchVal)
	}

	// sty_abb0f450: clicking the .col-id cell copies the story id to the
	// clipboard and short-circuits the row-expand handler. Assertions:
	//   - the row's .col-id cell briefly flips to "copied!" + gets
	//     the is-copied class (the visible-confirmation contract);
	//   - the row's detail row stays hidden (offsetParent === null);
	//   - 127.0.0.1 is a Chromium secure-context so navigator.clipboard
	//     is reachable — we don't read the clipboard back (headless
	//     permission gate), but the flash UI is the user-facing
	//     contract from the AC.
	// Real CDP click (not JS .click()) so the browser registers the user
	// activation navigator.clipboard.writeText requires; with the granted
	// clipboard permission the writeText resolves and the flash fires. Sleep
	// lets the async clipboard promise's .then(flash) run before we read.
	if err := chromedp.Run(bctx,
		chromedp.Click(`td.col-id[data-field="story-id-copy"]`, chromedp.ByQuery),
		chromedp.Sleep(150*time.Millisecond),
	); err != nil {
		t.Fatalf("click col-id: %v", err)
	}
	var copiedFlash struct {
		HasClass bool   `json:"hasClass"`
		Text     string `json:"text"`
		Expanded bool   `json:"expanded"`
	}
	if err := chromedp.Run(bctx, chromedp.Evaluate(`(() => {
		const cell = Array.from(document.querySelectorAll('td.col-id.is-copied'))[0];
		if (!cell) { return {hasClass:false,text:'',expanded:false}; }
		const row = cell.closest('tr.story-row');
		const id = row && row.dataset && row.dataset.id;
		const detail = id && document.querySelector('tr.story-detail[data-detail-for="'+id+'"]');
		return {
			hasClass: cell.classList.contains('is-copied'),
			text: cell.querySelector('code').textContent,
			expanded: !!(detail && detail.offsetParent !== null),
		};
	})()`, &copiedFlash)); err != nil {
		t.Fatalf("read copy state: %v", err)
	}
	if !copiedFlash.HasClass {
		t.Errorf("col-id click did not flip the cell to is-copied")
	}
	if copiedFlash.Text != "copied!" {
		t.Errorf("col-id click: cell text = %q, want %q", copiedFlash.Text, "copied!")
	}
	if copiedFlash.Expanded {
		t.Errorf("col-id click expanded the row — @click.stop missing")
	}
}

// TestStoryPanel_FilterBugs covers the three sty_f4a72a6e defects:
//
//  1. Repeated `tags:` tokens combine with OR (was AND); multi-tag
//     queries return the union of matched rows.
//  2. Removing a user-added chip (the x button on tags / order chips)
//     drops only that predicate, leaving siblings intact. Verified by
//     calling removeFromQuery directly via the panel's __test__ accessor.
//  3. Typing `order:<field>` sorts the table; dropping the order chip
//     restores the server-rendered DOM order.
func TestStoryPanel_FilterBugs(t *testing.T) {
	env := testbootstrap.SetUpWithServer(t)

	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	docStore := document.New(env.DB)
	ledStore := ledger.New(env.DB)
	verb.SetAuthStore(env.Store)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	verb.SetDocumentStore(docStore)
	verb.SetLedgerStore(ledStore)
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
		verb.SetDocumentStore(nil)
		verb.SetLedgerStore(nil)
	})

	ctx := context.Background()
	now := time.Date(2026, 5, 27, 4, 0, 0, 0, time.UTC)
	ws, err := wsStore.Create(ctx, "", "filter-bugs-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{
		WorkspaceID: ws.ID,
		Name:        "filter-bugs-project",
	}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	// Two epics, two stories each. Tag-OR test wants rows that match
	// EITHER epic; chip-x test wants to remove one of two tag tokens
	// and see the surviving filter narrow correctly.
	// Seeds carry an arbitrary `order:<v>` tag alongside `epic-order:<n>`
	// so the test can exercise both the canonical satellites prefix
	// (`epic-order`) and an alphanumeric prefix (`order` — vire's
	// convention, screenshot from sty_f4a72a6e). Values are picked to
	// catch a naive lexicographic sort: "2" < "10" requires the
	// localeCompare(numeric:true) path; "10" < "a" requires
	// digits-before-letters ordering.
	seeds := []struct {
		Name string
		Tags []string
	}{
		{"alpha changelog", []string{"epic:changelog", "epic-order:1", "order:10"}},
		{"beta changelog", []string{"epic:changelog", "epic-order:2", "order:2"}},
		{"gamma site-content", []string{"epic:site-content-refresh", "epic-order:1", "order:a"}},
		{"delta site-content", []string{"epic:site-content-refresh", "epic-order:2", "order:b"}},
	}
	status, priority := "backlog", "medium"
	for _, s := range seeds {
		tg := append([]string(nil), s.Tags...)
		req, _ := json.Marshal(verb.DocumentUpsertRequest{
			Type:      "story",
			ProjectID: pj.ID,
			Name:      s.Name,
			Status:    &status,
			Priority:  &priority,
			Tags:      &tg,
		})
		if _, err := verb.Dispatch(ctx, "document_upsert", req); err != nil {
			t.Fatalf("create %s: %v", s.Name, err)
		}
	}

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), chromedpHeadlessOpts()...)
	t.Cleanup(cancelAlloc)
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	t.Cleanup(cancelBrowser)
	bctx, cancelRun := context.WithTimeout(browserCtx, 60*time.Second)
	t.Cleanup(cancelRun)

	if err := chromedp.Run(bctx,
		chromedp.Navigate(env.ServerURL+"/login"),
		chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
		chromedp.Click(`button[data-action="dev-login-admin"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
		chromedp.Navigate(env.ServerURL+"/projects/"+pj.ID),
		chromedp.WaitVisible(`[data-field="panel-stories-chip-status-open"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("nav: %v", err)
	}

	// Bug 2: two tag tokens — expect the UNION (all four stories).
	if err := setStorySearch(bctx, "tags:epic:changelog tags:epic:site-content-refresh"); err != nil {
		t.Fatalf("set tag union: %v", err)
	}
	if err := chromedp.Run(bctx,
		chromedp.WaitVisible(`[data-field="panel-stories-chip-tags-epic:changelog"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-field="panel-stories-chip-tags-epic:site-content-refresh"]`, chromedp.ByQuery),
		chromedp.Sleep(150*time.Millisecond),
	); err != nil {
		t.Fatalf("wait both tag chips: %v", err)
	}
	titles, err := visibleRowTitles(bctx)
	if err != nil {
		t.Fatalf("read titles after tag union: %v", err)
	}
	for _, want := range []string{"alpha changelog", "beta changelog", "gamma site-content", "delta site-content"} {
		if !titles[want] {
			t.Errorf("tag OR: %q missing from union; visible=%v", want, titles)
		}
	}

	// Bug 3: click the x on one tag chip — only the other epic's
	// stories should remain visible. Driving via JS (not chromedp.Click)
	// to avoid CSS-selector quoting issues around the colons in the
	// chip data-field.
	if err := chromedp.Run(bctx, chromedp.Evaluate(`(() => {
		const chip = document.querySelector('[data-field="panel-stories-chip-tags-epic:changelog"]');
		const btn = chip && chip.querySelector('.panel-filter-chip-remove');
		if (!btn) { return false; }
		btn.click();
		return true;
	})()`, nil)); err != nil {
		t.Fatalf("click chip x: %v", err)
	}
	if err := waitChipAbsent(bctx, `[data-field="panel-stories-chip-tags-epic:changelog"]`); err != nil {
		t.Fatalf("changelog chip never cleared: %v", err)
	}
	if err := chromedp.Run(bctx, chromedp.Sleep(150*time.Millisecond)); err != nil {
		t.Fatalf("settle: %v", err)
	}
	titles, err = visibleRowTitles(bctx)
	if err != nil {
		t.Fatalf("read titles after chip-x: %v", err)
	}
	if !titles["gamma site-content"] || !titles["delta site-content"] {
		t.Errorf("chip-x: site-content rows should remain visible; got %v", titles)
	}
	if titles["alpha changelog"] || titles["beta changelog"] {
		t.Errorf("chip-x: changelog rows should be hidden after removing the changelog tag chip; got %v", titles)
	}

	// Bug 1a: type order:title — verify rows ordered alphabetically.
	// alpha → beta → delta → gamma is the ASCII title sort.
	// Server-authoritative: the prior chip-x committed a tag filter, so the
	// DOM holds only those rows. Commit order:title via navigation so the
	// server returns and sorts the full set (sty_1bd0098a) rather than
	// client-previewing over the narrowed DOM — sty_b7ba18b3.
	if err := chromedp.Run(bctx,
		chromedp.Navigate(env.ServerURL+"/projects/"+pj.ID+"?stories_q=order%3Atitle"),
		chromedp.WaitVisible(`[data-field="panel-stories-chip-order-title"]`, chromedp.ByQuery),
		chromedp.Sleep(150*time.Millisecond),
	); err != nil {
		t.Fatalf("order:title via navigation: %v", err)
	}
	titlesOrdered, err := orderedVisibleTitles(bctx)
	if err != nil {
		t.Fatalf("read ordered titles: %v", err)
	}
	wantSorted := []string{"alpha changelog", "beta changelog", "delta site-content", "gamma site-content"}
	if !equalSlices(titlesOrdered, wantSorted) {
		t.Errorf("order:title: got %v want %v", titlesOrdered, wantSorted)
	}

	// Bug 1 (alphanumeric tag-prefix sort): `order:order` is NOT an
	// orderFields key, so applyStoryOrder treats it as the tag prefix.
	// Seeds use values `10`, `2`, `a`, `b` — natural sort yields
	// `2 < 10 < a < b` (numeric-before-letters via localeCompare with
	// numeric:true). Repro of the operator screenshot from sty_f4a72a6e:
	// vire stories tagged `order:<n>` did not sort under `order:order`
	// before the fix (the panel only knew about `epic-order`).
	if err := chromedp.Run(bctx,
		chromedp.Click(`[data-action="panel-stories-clear-all"]`, chromedp.ByQuery),
		chromedp.Sleep(100*time.Millisecond),
	); err != nil {
		t.Fatalf("clear before order:order: %v", err)
	}
	if err := setStorySearch(bctx, "order:order"); err != nil {
		t.Fatalf("set order:order: %v", err)
	}
	if err := chromedp.Run(bctx,
		chromedp.WaitVisible(`[data-field="panel-stories-chip-order-order"]`, chromedp.ByQuery),
		chromedp.Sleep(150*time.Millisecond),
	); err != nil {
		t.Fatalf("wait order:order chip: %v", err)
	}
	titlesOrdered, err = orderedVisibleTitles(bctx)
	if err != nil {
		t.Fatalf("read order:order titles: %v", err)
	}
	wantNatural := []string{"beta changelog", "alpha changelog", "gamma site-content", "delta site-content"}
	if !equalSlices(titlesOrdered, wantNatural) {
		t.Errorf("order:order natural-sort: got %v want %v", titlesOrdered, wantNatural)
	}

	// Bug 1c: remove the order chip — rows return to their original
	// insertion order (newest-first: delta, gamma, beta, alpha given
	// the seed loop's chronological inserts).
	if err := chromedp.Run(bctx, chromedp.Evaluate(`(() => {
		const chip = document.querySelector('[data-field="panel-stories-chip-order-order"]');
		const btn = chip && chip.querySelector('.panel-filter-chip-remove');
		if (!btn) { return false; }
		btn.click();
		return true;
	})()`, nil)); err != nil {
		t.Fatalf("click order:order x: %v", err)
	}
	if err := waitChipAbsent(bctx, `[data-field="panel-stories-chip-order-order"]`); err != nil {
		t.Fatalf("order:order chip never cleared: %v", err)
	}
	if err := chromedp.Run(bctx, chromedp.Sleep(150*time.Millisecond)); err != nil {
		t.Fatalf("settle: %v", err)
	}

	// Bug 1d: re-apply order:title — sanity that the prior order:title
	// path still works after the natural-sort generalisation.
	if err := setStorySearch(bctx, "order:title"); err != nil {
		t.Fatalf("re-set order:title: %v", err)
	}
	if err := chromedp.Run(bctx,
		chromedp.WaitVisible(`[data-field="panel-stories-chip-order-title"]`, chromedp.ByQuery),
		chromedp.Sleep(150*time.Millisecond),
	); err != nil {
		t.Fatalf("wait order:title chip: %v", err)
	}

	// Bug 1b: remove the order chip — rows return to their original
	// insertion order (newest-first: delta, gamma, beta, alpha given
	// the seed loop's chronological inserts).
	if err := chromedp.Run(bctx, chromedp.Evaluate(`(() => {
		const chip = document.querySelector('[data-field="panel-stories-chip-order-title"]');
		const btn = chip && chip.querySelector('.panel-filter-chip-remove');
		if (!btn) { return false; }
		btn.click();
		return true;
	})()`, nil)); err != nil {
		t.Fatalf("click order x: %v", err)
	}
	if err := waitChipAbsent(bctx, `[data-field="panel-stories-chip-order-title"]`); err != nil {
		t.Fatalf("order chip never cleared: %v", err)
	}
	if err := chromedp.Run(bctx, chromedp.Sleep(150*time.Millisecond)); err != nil {
		t.Fatalf("settle: %v", err)
	}
	titlesOrdered, err = orderedVisibleTitles(bctx)
	if err != nil {
		t.Fatalf("read post-restore titles: %v", err)
	}
	// First item after restore should NOT be alpha (which would be the
	// case if the table stayed in alphabetical order). Stories list
	// newest-first; alpha was the first insert and therefore last in
	// the rendered order.
	if len(titlesOrdered) > 0 && titlesOrdered[0] == "alpha changelog" {
		t.Errorf("order restore failed — table is still sorted alphabetically: %v", titlesOrdered)
	}

	// Bug 3 (pure-logic sanity): exercise removeFromQuery via the
	// panel's __test__ accessor. Confirms the pure rewrite returns the
	// expected string for the common chip-x scenarios. Hits the same
	// code path the Alpine handler uses.
	var rewrites struct {
		DropOneTag       string `json:"drop_one_tag"`
		DropOrder        string `json:"drop_order"`
		DropOrderUnknown string `json:"drop_order_unknown"`
		DropSearch       string `json:"drop_search"`
		DropOneStatus    string `json:"drop_one_status"`
	}
	if err := chromedp.Run(bctx, chromedp.Evaluate(`(() => {
		const r = window.storyPanelFactory.__test__.removeFromQuery;
		return {
			drop_one_tag:       r("tags:epic:changelog tags:epic:other order:title", "tags", "epic:changelog"),
			drop_order:         r("tags:area:portal order:title", "order", "title"),
			drop_order_unknown: r("order:order", "order", "order"),
			drop_search:        r("tags:area:portal free text here", "search", ""),
			drop_one_status:    r("status:ready,review priority:high", "status", "ready"),
		};
	})()`, &rewrites)); err != nil {
		t.Fatalf("evaluate removeFromQuery: %v", err)
	}
	if rewrites.DropOneTag != "tags:epic:other order:title" {
		t.Errorf("drop_one_tag: got %q", rewrites.DropOneTag)
	}
	if rewrites.DropOrder != "tags:area:portal" {
		t.Errorf("drop_order: got %q", rewrites.DropOrder)
	}
	if rewrites.DropOrderUnknown != "" {
		t.Errorf("drop_order_unknown (order:order): got %q want \"\"", rewrites.DropOrderUnknown)
	}
	if rewrites.DropSearch != "tags:area:portal" {
		t.Errorf("drop_search: got %q", rewrites.DropSearch)
	}
	if rewrites.DropOneStatus != "status:review priority:high" {
		t.Errorf("drop_one_status: got %q", rewrites.DropOneStatus)
	}

	// Bug 3 (end-to-end repro from the operator's screenshot): typing
	// `order:order` must render as an `order:order` chip (NOT
	// `search:order:order` — that was the pre-fix free-text fallthrough),
	// and clicking the X must drop the chip + clear the search input.
	// Unknown order fields still produce a structural order chip so the
	// user can dismiss them; applyStoryOrder no-ops on unknown fields.
	// Reset to the clean page via navigation rather than the clear-all button:
	// after the prior chip-x the panel has no user chips, so clear-all is
	// hidden and chromedp.Click would block until the context deadline (the
	// 63s timeout this story fixes) — sty_b7ba18b3.
	if err := chromedp.Run(bctx,
		chromedp.Navigate(env.ServerURL+"/projects/"+pj.ID),
		chromedp.WaitVisible(`[data-field="panel-stories-chip-status-open"]`, chromedp.ByQuery),
		chromedp.Sleep(100*time.Millisecond),
	); err != nil {
		t.Fatalf("reset before order:order typo test: %v", err)
	}
	if err := setStorySearch(bctx, "order:order"); err != nil {
		t.Fatalf("set order:order: %v", err)
	}
	if err := chromedp.Run(bctx,
		chromedp.WaitVisible(`[data-field="panel-stories-chip-order-order"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("order:order chip never appeared: %v", err)
	}
	var wrongChipPresent bool
	if err := chromedp.Run(bctx,
		chromedp.Evaluate(`!!document.querySelector('[data-field="panel-stories-chip-search-order:order"]')`, &wrongChipPresent),
	); err != nil {
		t.Fatalf("probe wrong chip: %v", err)
	}
	if wrongChipPresent {
		t.Errorf("order:order rendered as search:order:order — parser regressed to free-text fallthrough")
	}
	if err := chromedp.Run(bctx, chromedp.Evaluate(`(() => {
		const chip = document.querySelector('[data-field="panel-stories-chip-order-order"]');
		const btn = chip && chip.querySelector('.panel-filter-chip-remove');
		if (!btn) { return false; }
		btn.click();
		return true;
	})()`, nil)); err != nil {
		t.Fatalf("click order:order chip x: %v", err)
	}
	if err := waitChipAbsent(bctx, `[data-field="panel-stories-chip-order-order"]`); err != nil {
		t.Fatalf("order:order chip never cleared (chip-x no-op regression): %v", err)
	}
	var searchAfter string
	if err := chromedp.Run(bctx,
		chromedp.Value(`[data-field="panel-stories-search"]`, &searchAfter, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("read search input post-x: %v", err)
	}
	if searchAfter != "" {
		t.Errorf("search input not cleared after chip x: got %q want empty", searchAfter)
	}
}

// orderedVisibleTitles reads the visible story-row titles in DOM order
// (after any reordering applyStoryOrder has done).
func orderedVisibleTitles(ctx context.Context) ([]string, error) {
	var titles []string
	err := chromedp.Run(ctx,
		chromedp.Evaluate(`
			Array.from(document.querySelectorAll('tr.story-row'))
				.filter(r => r.offsetParent !== null)
				.map(r => r.dataset.title || '')
		`, &titles),
	)
	return titles, err
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// chromedpHeadlessOpts returns the shared headless-Chrome allocator
// options used by every browser test in this package.
func chromedpHeadlessOpts() []chromedp.ExecAllocatorOption {
	return append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
	)
}

// setStorySearch writes `value` into the panel search input and fires
// the input event so x-model + every downstream x-show / x-for
// re-evaluates in one synchronous batch. SendKeys types one character
// at a time which lets row-level x-show race with the chip x-for
// during multi-token queries; this avoids that race entirely.
func setStorySearch(ctx context.Context, value string) error {
	js := `(() => {
		const el = document.querySelector('[data-field="panel-stories-search"]');
		if (!el) { return false; }
		el.value = ` + jsString(value) + `;
		el.dispatchEvent(new Event('input', { bubbles: true }));
		return true;
	})()`
	var ok bool
	return chromedp.Run(ctx, chromedp.Evaluate(js, &ok))
}

// jsString returns a JSON-encoded JS string literal, safe to embed
// inline in JS source.
func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// visibleRowTitles reads the DOM and returns a set of titles whose
// story-row offsetParent is non-null (i.e. Alpine has not display:none'd
// them via x-show).
func visibleRowTitles(ctx context.Context) (map[string]bool, error) {
	var titles []string
	err := chromedp.Run(ctx,
		chromedp.Evaluate(`
			Array.from(document.querySelectorAll('tr.story-row'))
				.filter(r => r.offsetParent !== null)
				.map(r => r.dataset.title || '')
		`, &titles),
	)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(titles))
	for _, t := range titles {
		out[t] = true
	}
	return out, nil
}

// waitChipAbsent polls until the chip matching sel is no longer present
// in the DOM, or the test ctx times out.
func waitChipAbsent(ctx context.Context, sel string) error {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var present bool
		if err := chromedp.Run(ctx,
			chromedp.Evaluate(`!!document.querySelector('`+sel+`')`, &present),
		); err != nil {
			return err
		}
		if !present {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return context.DeadlineExceeded
}
