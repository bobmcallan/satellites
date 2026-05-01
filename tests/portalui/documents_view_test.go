//go:build portalui

package portalui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/bobmcallan/satellites/internal/document"
)

// TestDocumentsView_TabFilters drives the documents browser (slice
// 11.4). Seeds two documents of different types; navigates to the
// list page; confirms the SSR rendered the right cards under the
// `?type=contract` filter; confirms the type tabs render.
func TestDocumentsView_TabFilters(t *testing.T) {
	h := StartHarness(t)

	now := time.Now().UTC()
	ctx := context.Background()
	if _, err := h.Documents.Create(ctx, document.Document{
		WorkspaceID: h.WorkspaceID,
		Type:        "contract",
		Scope:       "system",
		Name:        "plan",
		Status:      "active",
		Body:        "plan contract body",
	}, now); err != nil {
		t.Fatalf("seed contract: %v", err)
	}
	if _, err := h.Documents.Create(ctx, document.Document{
		WorkspaceID: h.WorkspaceID,
		Type:        "principle",
		Scope:       "system",
		Name:        "no-shortcuts",
		Status:      "active",
		Body:        "principle body",
	}, now); err != nil {
		t.Fatalf("seed principle: %v", err)
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
		chromedp.Navigate(h.BaseURL+"/documents?type=contract"),
		chromedp.WaitVisible(`[data-testid="documents-header"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-testid="type-tabs"]`, chromedp.ByQuery),
		chromedp.OuterHTML("html", &bodyHTML, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("navigate documents: %v", err)
	}

	for _, want := range []string{
		`data-testid="tab-all"`,
		`data-testid="tab-principle"`,
		`>plan<`,
	} {
		if !strings.Contains(bodyHTML, want) {
			t.Errorf("documents body missing %q", want)
		}
	}
	if strings.Contains(bodyHTML, ">no-shortcuts<") {
		t.Errorf("contract tab leaked principle row")
	}
}
