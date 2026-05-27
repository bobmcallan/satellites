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

	// AC 5: typing tags:area:portal narrows to two matching rows + adds
	// a user-set chip. Set the value directly + dispatch input rather
	// than chromedp.SendKeys to skip the per-character reactive churn
	// that races with the row-level x-show re-eval.
	if err := setStorySearch(bctx, "tags:area:portal"); err != nil {
		t.Fatalf("set search value: %v", err)
	}
	if err := chromedp.Run(bctx,
		chromedp.WaitVisible(`[data-field="panel-stories-chip-tags-area:portal"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("apply tag filter: %v", err)
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
