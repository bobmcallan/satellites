//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
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

// TestEngagementDot_Chromedp drives the QUALIFIED activity spinner
// (sty_07bb85b6, superseding the sty_25e2e8ac dot):
//   - an in-flight story with a live engaged session renders the spinner
//     AFTER the title (inside .story-row-title), with no row-height change
//   - a quiet engagement degrades (is-red at render past the threshold) and
//     the client aging pass turns a fresh one orange then red with NO reload
//   - an expired lease is stale but DORMANT (visible grey, not hidden) so a
//     long-running engagement keeps its affordance (sty_a7253546)
//   - a candidate-only story (read access) renders NO indicator
//   - a closed engagement renders NO indicator
//   - backlog and done rows render NO indicator even with engagement rows
//   - the tooltip reads "last activity Xm ago"
func TestEngagementDot_Chromedp(t *testing.T) {
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
	now := time.Now().UTC() // engagement rows must sit inside the fold's real-time window
	ws, err := wsStore.Create(ctx, "", "engdot-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "engdot-project"}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	mkStory := func(name string) string {
		t.Helper()
		req, _ := json.Marshal(verb.DocumentUpsertRequest{Type: "story", ProjectID: pj.ID, Name: name})
		raw, err := verb.Dispatch(ctx, "document_upsert", req)
		if err != nil {
			t.Fatalf("create story %s: %v", name, err)
		}
		var resp verb.DocumentUpsertResponse
		_ = json.Unmarshal(raw, &resp)
		return resp.Document.ID
	}
	// status_transition is the only status writer — the row projects onto the
	// story (sty_42d13ae4).
	setStatus := func(storyID, status string) {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{"to_status": status})
		req, _ := json.Marshal(verb.LedgerAppendRequest{
			StoryID: storyID, ProjectID: pj.ID, Kind: "status_transition",
			Body: "→ " + status, Payload: payload,
		})
		if _, err := verb.Dispatch(ctx, "ledger_append", req); err != nil {
			t.Fatalf("set status %s → %s: %v", storyID, status, err)
		}
	}
	engRow := func(storyID, kind, session string, lastSeen, leaseUntil time.Time, seq int64) {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{
			"phase": "in_progress", "seq": seq,
			"lease_until": leaseUntil.UTC().Format(time.RFC3339),
		})
		if _, err := ledStore.Append(ctx, ledger.AppendInput{
			StoryID: storyID, ProjectID: pj.ID, WorkspaceID: ws.ID, SessionID: session,
			Kind: kind, Body: "in_progress", Payload: payload, Actor: "usr_dev_admin",
		}, lastSeen); err != nil {
			t.Fatalf("append %s for %s: %v", kind, storyID, err)
		}
	}

	freshID := mkStory("engdot fresh")
	agedID := mkStory("engdot aged")
	staleID := mkStory("engdot stale")
	candID := mkStory("engdot candidate-only")
	closedID := mkStory("engdot closed")
	backlogID := mkStory("engdot backlog")
	doneID := mkStory("engdot done")

	for _, id := range []string{freshID, agedID, staleID, candID, closedID} {
		setStatus(id, "in_progress")
	}
	setStatus(doneID, "done") // backlogID stays at its created default

	lease := now.Add(2 * time.Hour)
	engRow(freshID, "engagement:tick", "sess-fresh", now, lease, 1)
	engRow(agedID, "engagement:tick", "sess-aged", now.Add(-16*time.Minute), lease, 1)
	engRow(staleID, "engagement:tick", "sess-stale", now.Add(-2*time.Minute), now.Add(-time.Minute), 1)
	engRow(candID, "engagement:candidate", "sess-cand", now, lease, 1)
	engRow(closedID, "engagement:tick", "sess-closed", now.Add(-time.Minute), lease, 1)
	engRow(closedID, "engagement:close", "sess-closed", now, lease, 2)
	engRow(backlogID, "engagement:tick", "sess-backlog", now, lease, 1)
	engRow(doneID, "engagement:tick", "sess-done", now, lease, 1)

	bctx := newBrowserCtx(t)
	if err := chromedp.Run(bctx,
		chromedp.Navigate(env.ServerURL+"/login"),
		chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
		chromedp.Click(`button[data-action="dev-login-admin"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
		chromedp.Navigate(env.ServerURL+"/projects/"+pj.ID+"?stories_q=status%3Aall"),
		chromedp.WaitVisible(`tr.story-row`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("login + navigate: %v", err)
	}

	eval := func(js string, out any) {
		t.Helper()
		if err := chromedp.Run(bctx, chromedp.Evaluate(js, out)); err != nil {
			t.Fatalf("evaluate %s: %v", js, err)
		}
	}
	spinnerClass := func(storyID string) string {
		var cls string
		eval(fmt.Sprintf(`(function(){var d=document.querySelector('tr[data-id=%q] .story-row-title .activity-spinner');return d?d.className:'<missing>'})()`, storyID), &cls)
		return cls
	}

	// Qualified rendering: only the working, in-flight stories wear a spinner.
	if cls := spinnerClass(freshID); cls != "activity-spinner" {
		t.Errorf("fresh engaged story must render the plain spinner after the title, got %q", cls)
	}
	if cls := spinnerClass(agedID); !strings.Contains(cls, "is-red") {
		t.Errorf("a 16-minute-quiet engagement must render is-red, got %q", cls)
	}
	if cls := spinnerClass(staleID); !strings.Contains(cls, "is-stale") {
		t.Errorf("an expired lease must mark is-stale, got %q", cls)
	}
	// sty_a7253546: a lapsed lease is DORMANT, not hidden — the dot stays
	// visible (grey) so a long-running engagement keeps its affordance.
	var staleDisplay string
	eval(fmt.Sprintf(`getComputedStyle(document.querySelector('tr[data-id=%q] .activity-spinner')).display`, staleID), &staleDisplay)
	if staleDisplay == "none" {
		t.Errorf("a stale spinner must remain visible (dormant), got display %q", staleDisplay)
	}
	var staleVisible bool
	eval(fmt.Sprintf(`(function(){var d=document.querySelector('tr[data-id=%q] .activity-spinner');return !!(d && d.offsetParent !== null)})()`, staleID), &staleVisible)
	if !staleVisible {
		t.Errorf("a stale (lapsed-lease) engaged story must still SHOW a dormant indicator")
	}
	for storyID, why := range map[string]string{
		candID:    "candidate-only (read access) story",
		closedID:  "closed engagement",
		backlogID: "backlog story",
		doneID:    "done story",
	} {
		if cls := spinnerClass(storyID); cls != "<missing>" {
			t.Errorf("%s must render NO indicator, got %q", why, cls)
		}
	}

	// Row height unchanged: the SAME row measures identically with and
	// without its spinner (remove it in-page and compare).
	var heights []float64
	eval(fmt.Sprintf(`(function(){var r=document.querySelector('tr[data-id=%q]');var before=r.offsetHeight;var d=r.querySelector('.activity-spinner');var p=d.parentNode;p.removeChild(d);var after=r.offsetHeight;p.appendChild(d);return [before, after]})()`, freshID), &heights)
	if len(heights) != 2 || heights[0] != heights[1] {
		t.Errorf("spinner must not change the row height: %v", heights)
	}

	// Tooltip honesty: "last activity Xm ago".
	var title string
	eval(fmt.Sprintf(`document.querySelector('tr[data-id=%q] .activity-spinner').title`, agedID), &title)
	if !strings.HasPrefix(title, "last activity ") || !strings.HasSuffix(title, "m ago") {
		t.Errorf("tooltip must read 'last activity Xm ago', got %q", title)
	}

	// No-reload aging: drive the aging pass with a future now — the fresh
	// spinner must degrade to orange, then red, with no navigation.
	var cls string
	eval(fmt.Sprintf(`(function(){window.satAgeEngagementDots(Date.now()+6*60*1000);return document.querySelector('tr[data-id=%q] .activity-spinner').className})()`, freshID), &cls)
	if !strings.Contains(cls, "is-orange") {
		t.Errorf("fresh spinner must turn is-orange past the 5-min threshold, got %q", cls)
	}
	eval(fmt.Sprintf(`(function(){window.satAgeEngagementDots(Date.now()+16*60*1000);return document.querySelector('tr[data-id=%q] .activity-spinner').className})()`, freshID), &cls)
	if !strings.Contains(cls, "is-red") {
		t.Errorf("fresh spinner must turn is-red past the 15-min threshold, got %q", cls)
	}
}
