//go:build portalui

package portalui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
)

// firstProjectID navigates to /projects and reads the first project's id
// from the rendered table. The harness seeds a default project for the
// dev user, so this returns a valid id whenever installSessionCookie
// has been called for that user.
func firstProjectID(ctx context.Context, h *Harness) (string, error) {
	var id string
	err := chromedp.Run(ctx,
		chromedp.Navigate(h.BaseURL+"/projects"),
		chromedp.WaitVisible(`table.data-table tbody tr`, chromedp.ByQuery),
		chromedp.Evaluate(`(() => {
			const code = document.querySelector('table.data-table tbody tr td:nth-of-type(2) code');
			return code ? code.textContent.trim() : '';
		})()`, &id),
	)
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf("no project id found in /projects table")
	}
	return id, nil
}

// TestProjectDetail_SectionOrder (story_25695308 AC3+AC4) — verifies
// the new layout: project meta first, search row, stories, documents,
// configuration last.
func TestProjectDetail_SectionOrder(t *testing.T) {
	h := StartHarness(t)

	parent, cancel := withTimeout(context.Background(), browserDeadline)
	defer cancel()
	browserCtx, cancelBrowser := newChromedpContext(t, parent)
	defer cancelBrowser()

	if err := installSessionCookie(browserCtx, h); err != nil {
		t.Fatalf("install session: %v", err)
	}
	projID, err := firstProjectID(browserCtx, h)
	if err != nil {
		t.Fatalf("locate project: %v", err)
	}

	var html string
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(h.BaseURL+"/projects/"+projID),
		chromedp.WaitVisible(`[data-testid="panel-meta"]`, chromedp.ByQuery),
		chromedp.OuterHTML("html", &html, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("navigate /projects/%s: %v", projID, err)
	}

	// story_59b11d8c moved the workspace-search out of project_detail
	// into the dedicated /projects/<id>/stories page.
	order := []string{
		`data-testid="panel-meta"`,
		`data-testid="panel-stories"`,
		`data-testid="panel-documents"`,
		`data-testid="panel-configuration"`,
	}
	prev := -1
	for _, marker := range order {
		idx := strings.Index(html, marker)
		if idx < 0 {
			t.Errorf("project_detail body missing %q", marker)
			continue
		}
		if idx < prev {
			t.Errorf("section order violation: %q (offset %d) before previous (offset %d)", marker, idx, prev)
		}
		prev = idx
	}
}

// TestProjectDetail_SearchClearButton was deleted in story_59b11d8c when
// the cross-entity workspace-search moved out of project_detail. The
// V3-style search lives on /projects/<id>/stories now and is covered by
// internal/portal/stories_list_search_test.go.

// TestProjectDetail_SectionToggle (story_25695308 AC5+AC6) — clicking
// a section header collapses it and the state is preserved across page
// reloads via sessionStorage.
func TestProjectDetail_SectionToggle(t *testing.T) {
	h := StartHarness(t)

	parent, cancel := withTimeout(context.Background(), browserDeadline)
	defer cancel()
	browserCtx, cancelBrowser := newChromedpContext(t, parent)
	defer cancelBrowser()

	if err := installSessionCookie(browserCtx, h); err != nil {
		t.Fatalf("install session: %v", err)
	}
	projID, err := firstProjectID(browserCtx, h)
	if err != nil {
		t.Fatalf("locate project: %v", err)
	}

	var bodyVisibleAfterClick, bodyVisibleAfterReload bool
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(h.BaseURL+"/projects/"+projID),
		chromedp.WaitVisible(`[data-testid="panel-stories"] .panel-body`, chromedp.ByQuery),
		// Click the stories section header to collapse it.
		chromedp.Click(`[data-testid="panel-stories"] .panel-header`, chromedp.ByQuery),
		chromedp.Evaluate(`new Promise(r => setTimeout(r, 100))`, nil, chromedp.EvalAsValue),
		chromedp.Evaluate(`(() => {
			const body = document.querySelector('[data-testid="panel-stories"] .panel-body');
			return body && body.offsetParent !== null;
		})()`, &bodyVisibleAfterClick),
		// Reload the page and assert the collapsed state persists.
		chromedp.Reload(),
		chromedp.WaitVisible(`[data-testid="panel-stories"]`, chromedp.ByQuery),
		chromedp.Evaluate(`new Promise(r => setTimeout(r, 100))`, nil, chromedp.EvalAsValue),
		chromedp.Evaluate(`(() => {
			const body = document.querySelector('[data-testid="panel-stories"] .panel-body');
			return body && body.offsetParent !== null;
		})()`, &bodyVisibleAfterReload),
	); err != nil {
		t.Fatalf("section toggle: %v", err)
	}

	if bodyVisibleAfterClick {
		t.Errorf("stories panel-body still visible after toggle; expected hidden")
	}
	if bodyVisibleAfterReload {
		t.Errorf("stories panel-body visible after reload; sessionStorage state not restored")
	}
}
