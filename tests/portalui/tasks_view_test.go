//go:build portalui

package portalui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/bobmcallan/satellites/internal/task"
)

// TestTasksView_HappyPath_LiveStatusFlip drives the task queue page
// (slice 11.2). Seeds an enqueued + in_flight task, navigates,
// confirms the SSR rendered both columns, then publishes a
// task.closed event over the harness websocket and waits for the
// in_flight card to move to the recently-closed column without a
// page refresh.
func TestTasksView_HappyPath_LiveStatusFlip(t *testing.T) {
	h := StartHarness(t)

	now := time.Now().UTC()
	ctx := context.Background()
	enq, err := h.Tasks.Enqueue(ctx, task.Task{
		WorkspaceID: h.WorkspaceID,
		Origin:      task.OriginScheduled,
		Priority:    task.PriorityHigh,
		Status:      task.StatusEnqueued,
	}, now)
	if err != nil {
		t.Fatalf("seed enqueued: %v", err)
	}
	claimed, err := h.Tasks.Claim(ctx, "worker_chromedp", []string{h.WorkspaceID}, now.Add(time.Second))
	if err != nil {
		t.Fatalf("claim: %v", err)
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
		chromedp.Navigate(h.BaseURL+"/tasks"),
		chromedp.WaitVisible(`[data-testid="column-in-flight"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-testid="column-enqueued"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-testid="column-closed"]`, chromedp.ByQuery),
		chromedp.OuterHTML("html", &bodyHTML, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("navigate tasks page: %v", err)
	}

	for _, want := range []string{
		`data-testid="task-card-` + claimed.ID + `"`,
		`worker_chromedp`,
		`data-testid="closed-empty-ssr"`,
	} {
		if !strings.Contains(bodyHTML, want) {
			t.Errorf("SSR body missing %q", want)
		}
	}
	// The enqueued task was claimed before navigation, so it should be
	// in the in_flight column, not enqueued. The enqueued column may
	// or may not have an empty marker depending on store ordering;
	// don't assert that.
	if !strings.Contains(bodyHTML, claimed.ID) {
		t.Errorf("claimed task id %q missing from SSR body", claimed.ID)
	}
	_ = enq // keep reference for clarity; the claim mutated this row in place

	if err := waitForIndicatorState(browserCtx, "live", 10*time.Second); err != nil {
		t.Fatalf("wait initial live: %v", err)
	}

	closedAt := time.Now().UTC()
	closedPayload := map[string]any{
		"id":           claimed.ID,
		"workspace_id": h.WorkspaceID,
		"origin":       claimed.Origin,
		"status":       task.StatusClosed,
		"priority":     claimed.Priority,
		"claimed_by":   claimed.ClaimedBy,
		"completed_at": closedAt.Format(time.RFC3339),
		"outcome":      task.OutcomeSuccess,
	}
	h.PublishEvent("task.closed", closedPayload)

	// Poll for the card to land in the recently-closed column.
	closedSelector := `document.querySelector('[data-testid="column-closed"] [data-testid="task-card-` + claimed.ID + `"]') !== null`
	if err := chromedp.Run(browserCtx,
		chromedp.Poll(closedSelector, nil, chromedp.WithPollingTimeout(8*time.Second)),
	); err != nil {
		t.Fatalf("closed card never landed in recently-closed column: %v", err)
	}
}
