//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
	"github.com/chromedp/chromedp"
)

// TestChangelogUpsert_WritePath_Chromedp proves the restored runtime write path
// (sty_1777fbeb). The changelog collapsed into a system-scope type:changelog
// document and lost its write verbs; this asserts the verb-layer allowance the
// `satellites changelog upsert` command relies on:
//   - a global admin OFF the MCP surface creates the entry via document_upsert,
//   - the same write over the MCP transport is refused (content stays off MCP),
//   - a non-admin off the MCP surface is refused (system stays read-only),
//   - the created entry then renders on the public /changelog page —
//
// end-to-end, no SQL seed and no redeploy.
func TestChangelogUpsert_WritePath_Chromedp(t *testing.T) {
	env := testbootstrap.SetUpWithServer(t)
	docStore := document.New(env.DB)
	verb.SetAuthStore(env.Store)
	verb.SetDocumentStore(docStore)
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetDocumentStore(nil)
	})

	const marker = "Runtime changelog write path restored."
	body := json.RawMessage(`{"type":"changelog","scope":"system","name":"cl_writepath",` +
		`"body":"# Write path\n\n` + marker + `",` +
		`"tags":["changelog","service:satellites","version_from:v0.0.256","version_to:v0.0.272","effective_date:2026-06-17"]}`)

	admin := &auth.User{ID: "usr_dev_admin", Role: auth.RoleAdmin}

	// Admin off the MCP surface (the CLI / HTTP exec path) → the write succeeds.
	adminCLI := auth.WithUser(context.Background(), admin)
	if _, err := verb.Get("document_upsert").Invoke(adminCLI, body); err != nil {
		t.Fatalf("admin CLI changelog upsert should succeed, got: %v", err)
	}

	// The same write over the MCP transport → refused (ErrForbidden).
	adminMCP := auth.WithUser(auth.WithTransport(context.Background(), auth.TransportMCP), admin)
	if _, err := verb.Get("document_upsert").Invoke(adminMCP, body); err == nil || !errors.Is(err, verb.ErrForbidden) {
		t.Fatalf("MCP changelog upsert should be ErrForbidden, got: %v", err)
	}

	// A non-admin off the MCP surface → refused (system scope stays read-only).
	memberCLI := auth.WithUser(context.Background(), &auth.User{ID: "usr_member"})
	if _, err := verb.Get("document_upsert").Invoke(memberCLI, body); err == nil {
		t.Fatalf("non-admin changelog upsert should be refused, but it succeeded")
	}

	// The admin-created entry renders on the public /changelog page (no seed).
	bctx := newBrowserCtx(t)
	if err := chromedp.Run(bctx,
		chromedp.Navigate(env.ServerURL+"/changelog"),
		chromedp.WaitVisible(`[data-section="changelog-group"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("navigate to /changelog: %v", err)
	}
	var content string
	if err := chromedp.Run(bctx,
		chromedp.Evaluate(`(document.querySelector('[data-service="satellites"] [data-field="content"]')||{}).textContent || ''`, &content),
	); err != nil {
		t.Fatalf("read entry: %v", err)
	}
	if !strings.Contains(content, marker) {
		t.Errorf("/changelog should show the upserted entry; content=%q", content)
	}
}
