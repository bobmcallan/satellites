//go:build portalui

package portalui

import (
	"context"
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
)

// TestProjects_DevUserSeesHarnessProject covers the post-sty_c975ebeb
// shape: production no longer auto-seeds a per-user Default project, but
// the portalui harness creates one explicit project (HarnessProjectName)
// so the SSR list view still has data to render. The test asserts the
// harness project appears and the empty-state copy does not.
func TestProjects_DevUserSeesHarnessProject(t *testing.T) {
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
		t.Errorf("/projects rendered %d rows; want ≥1 (harness project)", rowCount)
	}
	if !strings.Contains(bodyText, HarnessProjectName) {
		t.Errorf("/projects missing harness project %q; bodyText=%s", HarnessProjectName, bodyText)
	}
	if strings.Contains(bodyText, "You don't own any projects yet") {
		t.Errorf("/projects shows empty-state copy when harness project should be visible; bodyText=%s", bodyText)
	}
}
