//go:build portalui

package portalui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/bobmcallan/satellites/internal/repo"
)

// TestRepoView_HeaderRenders drives the repo view (slice 11.5). Seeds
// a project + a repo row and confirms the header + symbol-search panel
// render server-side.
func TestRepoView_HeaderRenders(t *testing.T) {
	h := StartHarness(t)

	now := time.Now().UTC()
	ctx := context.Background()
	proj, err := h.Projects.Create(ctx, h.UserID, h.WorkspaceID, "alpha", now)
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := h.Repos.Create(ctx, repo.Repo{
		WorkspaceID:   h.WorkspaceID,
		ProjectID:     proj.ID,
		GitRemote:     "git@example.com:alpha/main.git",
		DefaultBranch: "main",
		HeadSHA:       "abcdef0123",
		Status:        repo.StatusActive,
		SymbolCount:   42,
		FileCount:     7,
	}, now); err != nil {
		t.Fatalf("seed repo: %v", err)
	}

	parent, cancel := withTimeout(context.Background(), browserDeadline)
	defer cancel()
	browserCtx, cancelBrowser := newChromedpContext(t, parent)
	defer cancelBrowser()

	if err := installFastFlag(browserCtx); err != nil {
		t.Fatalf("install fast flag: %v", err)
	}
	if err := installSessionCookie(browserCtx, h); err != nil {
		t.Fatalf("install cookie: %v", err)
	}

	var bodyHTML string
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(h.BaseURL+"/repo"),
		chromedp.WaitVisible(`[data-testid="repo-header"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-testid="symbol-search-panel"]`, chromedp.ByQuery),
		chromedp.OuterHTML("html", &bodyHTML, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("navigate /repo: %v", err)
	}

	for _, want := range []string{
		"git@example.com:alpha/main.git",
		"abcdef0123",
		`data-testid="commits-empty"`,
		`data-testid="diff-empty"`,
	} {
		if !strings.Contains(bodyHTML, want) {
			t.Errorf("repo body missing %q", want)
		}
	}
}
