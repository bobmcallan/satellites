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

// TestEngagementDot_Chromedp drives the portal processing indicator
// (sty_25e2e8ac): a story with a fresh engagement renders the green dot, a
// quiet one renders red past the threshold, an expired lease renders stale,
// the tooltip reads "last activity Xm ago", and the client-side aging pass
// degrades a fresh dot with NO reload.
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
	freshID := mkStory("engdot fresh")
	agedID := mkStory("engdot aged")
	staleID := mkStory("engdot stale")

	engage := func(storyID, session string, lastSeen, leaseUntil time.Time, seq int64) {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{
			"phase": "in_progress", "seq": seq,
			"lease_until": leaseUntil.UTC().Format(time.RFC3339),
		})
		if _, err := ledStore.Append(ctx, ledger.AppendInput{
			StoryID: storyID, ProjectID: pj.ID, WorkspaceID: ws.ID, SessionID: session,
			Kind: "engagement:tick", Body: "in_progress", Payload: payload, Actor: "usr_dev_admin",
		}, lastSeen); err != nil {
			t.Fatalf("append engagement for %s: %v", storyID, err)
		}
	}
	engage(freshID, "sess-fresh", now, now.Add(2*time.Hour), 1)
	engage(agedID, "sess-aged", now.Add(-16*time.Minute), now.Add(2*time.Hour), 1)
	engage(staleID, "sess-stale", now.Add(-2*time.Minute), now.Add(-time.Minute), 1)

	bctx := newBrowserCtx(t)
	if err := chromedp.Run(bctx,
		chromedp.Navigate(env.ServerURL+"/login"),
		chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
		chromedp.Click(`button[data-action="dev-login-admin"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
		chromedp.Navigate(env.ServerURL+"/projects/"+pj.ID),
		chromedp.WaitVisible(`tr.story-row`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("login + navigate: %v", err)
	}

	classOf := func(storyID string) string {
		t.Helper()
		var cls string
		js := fmt.Sprintf(`(function(){var d=document.querySelector('tr[data-id=%q] .engagement-dot');return d?d.className:'<missing>'})()`, storyID)
		if err := chromedp.Run(bctx, chromedp.Evaluate(js, &cls)); err != nil {
			t.Fatalf("evaluate class for %s: %v", storyID, err)
		}
		return cls
	}

	if cls := classOf(freshID); cls != "engagement-dot" {
		t.Errorf("fresh engagement must render the plain green dot, got %q", cls)
	}
	if cls := classOf(agedID); !strings.Contains(cls, "is-red") {
		t.Errorf("a 16-minute-quiet engagement must render is-red, got %q", cls)
	}
	if cls := classOf(staleID); !strings.Contains(cls, "is-stale") {
		t.Errorf("an expired lease must render is-stale, got %q", cls)
	}

	// Tooltip honesty (AC5): "last activity Xm ago".
	var title string
	if err := chromedp.Run(bctx, chromedp.Evaluate(
		fmt.Sprintf(`document.querySelector('tr[data-id=%q] .engagement-dot').title`, agedID), &title)); err != nil {
		t.Fatalf("evaluate title: %v", err)
	}
	if !strings.HasPrefix(title, "last activity ") || !strings.HasSuffix(title, "m ago") {
		t.Errorf("tooltip must read 'last activity Xm ago', got %q", title)
	}

	// No-reload aging (AC2): drive the aging pass with a future now — the
	// fresh dot must degrade to orange, then red, with no navigation.
	var cls string
	if err := chromedp.Run(bctx, chromedp.Evaluate(fmt.Sprintf(
		`(function(){window.satAgeEngagementDots(Date.now()+6*60*1000);return document.querySelector('tr[data-id=%q] .engagement-dot').className})()`,
		freshID), &cls)); err != nil {
		t.Fatalf("aging pass (orange): %v", err)
	}
	if !strings.Contains(cls, "is-orange") {
		t.Errorf("fresh dot must turn is-orange past the 5-min threshold, got %q", cls)
	}
	if err := chromedp.Run(bctx, chromedp.Evaluate(fmt.Sprintf(
		`(function(){window.satAgeEngagementDots(Date.now()+16*60*1000);return document.querySelector('tr[data-id=%q] .engagement-dot').className})()`,
		freshID), &cls)); err != nil {
		t.Fatalf("aging pass (red): %v", err)
	}
	if !strings.Contains(cls, "is-red") {
		t.Errorf("fresh dot must turn is-red past the 15-min threshold, got %q", cls)
	}
}
