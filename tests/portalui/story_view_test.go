//go:build portalui

package portalui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/story"
)

// TestStoryView_HappyPath_LiveVerdict drives the upgraded story view
// (slice 11.1, story_3b450d9e). Seeds a story, navigates to it,
// confirms the panels render server-side, then publishes a
// kind:verdict ledger event over the harness websocket and waits for
// the verdict panel + delivery strip to update without a refresh.
func TestStoryView_HappyPath_LiveVerdict(t *testing.T) {
	h := StartHarness(t)

	now := time.Now().UTC()
	proj, err := h.Projects.Create(context.Background(), h.UserID, h.WorkspaceID, "alpha", now)
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	s, err := h.Stories.Create(context.Background(), story.Story{
		ProjectID:          proj.ID,
		WorkspaceID:        h.WorkspaceID,
		Title:              "story-view chromedp fixture",
		Description:        "fixture description",
		AcceptanceCriteria: "fixture AC",
		Status:             "in_progress",
		Priority:           "high",
		Category:           "feature",
		Tags:               []string{"source:ui-design.md#story-view", "epic:portal-views"},
		CreatedBy:          h.UserID,
	}, now)
	if err != nil {
		t.Fatalf("seed story: %v", err)
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

	storyURL := h.BaseURL + "/projects/" + proj.ID + "/stories/" + s.ID

	// Navigate, confirm the SSR rendered the five panels.
	var bodyHTML string
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(storyURL),
		chromedp.WaitVisible(`[data-testid="ci-timeline-panel"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-testid="verdict-panel"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-testid="repo-provenance-panel"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-testid="excerpts-panel"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-testid="source-docs-panel"]`, chromedp.ByQuery),
		chromedp.OuterHTML("html", &bodyHTML, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("navigate story view: %v", err)
	}

	for _, want := range []string{
		`source:ui-design.md#story-view`,
		`ui-design.md §story-view`,
	} {
		if !strings.Contains(bodyHTML, want) {
			t.Errorf("SSR body missing %q", want)
		}
	}

	// Wait for the websocket to land in `live` so subsequent PublishEvent
	// calls are observed by the page's storyView() Alpine factory.
	if err := waitForIndicatorState(browserCtx, "live", 10*time.Second); err != nil {
		t.Fatalf("wait initial live: %v", err)
	}

	// Publish a kind:verdict ledger.created event scoped to this story.
	verdictPayload := map[string]any{
		"id":          "ldg_test_verdict",
		"type":        string(ledger.TypeVerdict),
		"tags":        []string{"kind:verdict", "phase:story_close"},
		"story_id":    s.ID,
		"contract_id": "ci_chromedp",
		"content":     "approved-by-test",
		"created_at":  time.Now().UTC().Format(time.RFC3339),
		"structured": map[string]any{
			"verdict":   "approved",
			"score":     5,
			"reasoning": "all ACs satisfied",
		},
	}
	h.PublishEvent("ledger.created", verdictPayload)

	// Poll for the verdict row appearing without a refresh.
	if err := chromedp.Run(browserCtx,
		chromedp.Poll(
			`document.querySelector('[data-testid^="verdict-row-"]') !== null`,
			nil,
			chromedp.WithPollingTimeout(8*time.Second),
		),
	); err != nil {
		t.Fatalf("verdict row never appeared: %v", err)
	}

	// Confirm delivery strip flipped to `delivered` resolution.
	var resolutionText string
	if err := chromedp.Run(browserCtx,
		chromedp.Text(`.resolution-pill`, &resolutionText, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("read resolution: %v", err)
	}
	if !strings.Contains(resolutionText, "delivered") {
		t.Errorf("resolution-pill text = %q, want contains 'delivered'", resolutionText)
	}
}
