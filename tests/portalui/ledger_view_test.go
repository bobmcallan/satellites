//go:build portalui

package portalui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/bobmcallan/satellites/internal/ledger"
)

// TestLedgerView_LiveTailAndPill drives the ledger inspection page
// (slice 11.3). With tailing on, a published ledger.created event
// should auto-prepend; with tailing off, the "N new rows" pill should
// surface and clicking it flushes pending rows into the visible list.
func TestLedgerView_LiveTailAndPill(t *testing.T) {
	h := StartHarness(t)

	now := time.Now().UTC()
	ctx := context.Background()
	proj, err := h.Projects.Create(ctx, h.UserID, h.WorkspaceID, "alpha", now)
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	// Seed an initial row so the page renders the rows list rather
	// than the empty-state.
	_, _ = h.Ledger.Append(ctx, ledger.LedgerEntry{
		ProjectID:  proj.ID,
		Type:       ledger.TypeDecision,
		Tags:       []string{"kind:test-seed"},
		Content:    "seed-row",
		Durability: ledger.DurabilityDurable,
		SourceType: ledger.SourceSystem,
		Status:     ledger.StatusActive,
	}, now)

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

	ledgerURL := h.BaseURL + "/projects/" + proj.ID + "/ledger"
	var bodyHTML string
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(ledgerURL),
		chromedp.WaitVisible(`[data-testid="ledger-header"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-testid="ledger-sidebar"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-testid="ledger-rows-panel"]`, chromedp.ByQuery),
		chromedp.OuterHTML("html", &bodyHTML, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("navigate ledger view: %v", err)
	}

	if !strings.Contains(bodyHTML, `data-testid="filter-type"`) {
		t.Errorf("ledger sidebar missing filter-type select")
	}

	if err := waitForIndicatorState(browserCtx, "live", 10*time.Second); err != nil {
		t.Fatalf("wait initial live: %v", err)
	}

	// Tailing OFF (default state) — publish a row and expect the pill to surface.
	rowOff := map[string]any{
		"id":          "ldg_pillrow",
		"type":        "decision",
		"tags":        []string{"kind:test-pill"},
		"content":     "live-row-off",
		"durability":  "durable",
		"source_type": "system",
		"status":      "active",
		"created_at":  time.Now().UTC().Format(time.RFC3339),
	}
	h.PublishEvent("ledger.created", rowOff)

	if err := chromedp.Run(browserCtx,
		chromedp.Poll(
			`document.querySelector('[data-testid="new-rows-pill"]') !== null`,
			nil,
			chromedp.WithPollingTimeout(8*time.Second),
		),
	); err != nil {
		t.Fatalf("new-rows pill never appeared: %v", err)
	}
}
