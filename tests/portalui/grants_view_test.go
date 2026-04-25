//go:build portalui

package portalui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/rolegrant"
)

// TestGrantsView_RendersAndShowsRevoke drives the /grants page (slice
// 6.7). Seeds a role doc, an agent doc, and a grant; asserts the SSR
// rendered the row + the admin (workspace owner) sees the active
// Revoke button.
func TestGrantsView_RendersAndShowsRevoke(t *testing.T) {
	h := StartHarness(t)

	now := time.Now().UTC()
	ctx := context.Background()
	role, err := h.Documents.Create(ctx, document.Document{
		WorkspaceID: h.WorkspaceID, Type: "role", Scope: "system",
		Name: "role_x", Status: "active", Body: "role x body",
	}, now)
	if err != nil {
		t.Fatalf("seed role: %v", err)
	}
	agent, err := h.Documents.Create(ctx, document.Document{
		WorkspaceID: h.WorkspaceID, Type: "agent", Scope: "system",
		Name: "agent_x", Status: "active", Body: "agent x body",
	}, now)
	if err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	g, err := h.Grants.Create(ctx, rolegrant.RoleGrant{
		WorkspaceID: h.WorkspaceID,
		RoleID:      role.ID, AgentID: agent.ID,
		GranteeKind: "session", GranteeID: "sess_chromedp",
	}, now)
	if err != nil {
		t.Fatalf("seed grant: %v", err)
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
		chromedp.Navigate(h.BaseURL+"/grants"),
		chromedp.WaitVisible(`[data-testid="grants-page"]`, chromedp.ByQuery),
		chromedp.OuterHTML("html", &bodyHTML, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("navigate /grants: %v", err)
	}

	// SSR rendered the seeded row.
	if !strings.Contains(bodyHTML, g.ID) {
		t.Errorf("grants body missing seeded grant id %s", g.ID)
	}
	// Owner of the workspace is admin → revoke button (active form) renders.
	if !strings.Contains(bodyHTML, "revoke") {
		t.Errorf("grants body missing revoke button")
	}
}

// TestRolesPage_RendersHeader covers /roles SSR.
func TestRolesPage_RendersHeader(t *testing.T) {
	h := StartHarness(t)

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

	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(h.BaseURL+"/roles"),
		chromedp.WaitVisible(`[data-testid="roles-page"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("navigate /roles: %v", err)
	}
}
