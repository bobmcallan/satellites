//go:build portalui

package portalui

import (
	"context"
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
)

// TestProjects_DevUserSeesDefaultProject covers AC2+AC3 of story_0f415ab3:
// after dev-signin the /projects panel renders at least one project row
// (the per-user default seeded by project.EnsureDefault) instead of the
// empty-state copy.
func TestProjects_DevUserSeesDefaultProject(t *testing.T) {
	h := StartHarness(t)

	parent, cancel := withTimeout(context.Background(), browserDeadline)
	defer cancel()
	browserCtx, cancelBrowser := newChromedpContext(t, parent)
	defer cancelBrowser()

	if err := installSessionCookie(browserCtx, h); err != nil {
		t.Fatalf("install cookie: %v", err)
	}

	var bodyText string
	var rowCount int
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(h.BaseURL+"/projects"),
		chromedp.WaitVisible(`section.panel-headed`, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelectorAll('table.data-table tbody tr').length`, &rowCount),
		chromedp.Text(`section.panel-headed`, &bodyText, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("navigate /projects: %v", err)
	}

	if rowCount < 1 {
		t.Errorf("/projects rendered %d rows; want ≥1 (per-user default seed)", rowCount)
	}
	if strings.Contains(bodyText, "You don't own any projects yet") {
		t.Errorf("/projects shows empty-state copy for dev-user; bodyText=%s", bodyText)
	}
}
