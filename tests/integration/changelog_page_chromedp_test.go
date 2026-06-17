//go:build integration

package integration_test

import (
	"testing"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
	"github.com/chromedp/chromedp"
	"github.com/lib/pq"
)

// TestChangelogPage_Chromedp drives the /changelog portal page after the
// changelog collapse (epic:satellites-backbone, Workstream C): changelog entries
// are now type:changelog documents (typed fields as tags, content as the body),
// and the page reads them through the generic document surface. It seeds two
// entries under different services and asserts both groups + their content
// render — proving the dedicated table/verbs are gone yet the page still works.
func TestChangelogPage_Chromedp(t *testing.T) {
	env := testbootstrap.SetUpWithServer(t)

	docStore := document.New(env.DB)
	verb.SetAuthStore(env.Store)
	verb.SetDocumentStore(docStore)
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetDocumentStore(nil)
	})

	// docStore is wired into verb above so the handler's document_list/get
	// dispatch resolves; the rows are seeded directly below.

	// Seed a type:changelog document directly, exactly as migration 0044 does:
	// the typed fields ride as tags, the content is the latest-version body, at
	// system scope. This validates the migration's produced shape end-to-end.
	seedEntry := func(name, service, versionTo, content string) {
		t.Helper()
		tags := []string{"changelog", "service:" + service, "version_from:", "version_to:" + versionTo}
		if _, err := env.DB.Exec(
			`INSERT INTO documents (id, scope, name, latest_version, type, tags, status, created_at, updated_at)
			 VALUES ($1,'system',$1,1,'changelog',$2,'active',now(),now())`, name, pq.Array(tags)); err != nil {
			t.Fatalf("seed changelog doc %s: %v", name, err)
		}
		if _, err := env.DB.Exec(
			`INSERT INTO document_versions (document_id, version, body, status, created_at, created_by)
			 VALUES ($1,1,$2,'active',now(),'system:test')`, name, content); err != nil {
			t.Fatalf("seed changelog version %s: %v", name, err)
		}
	}
	seedEntry("cl_test_cli", "satellites", "0.0.260", "# CLI release\n\nGoal-keeper Stop hook shipped.")
	seedEntry("cl_test_srv", "satellites-server", "0.0.272", "Server-side changelog collapse.")

	bctx := newBrowserCtx(t)
	// /changelog is public — no login required.
	if err := chromedp.Run(bctx,
		chromedp.Navigate(env.ServerURL+"/changelog"),
		chromedp.WaitVisible(`[data-section="changelog-group"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("navigate to /changelog: %v", err)
	}

	count := func(sel string) int {
		var n int
		if err := chromedp.Run(bctx, chromedp.Evaluate(`document.querySelectorAll('`+sel+`').length`, &n)); err != nil {
			t.Fatalf("count %s: %v", sel, err)
		}
		return n
	}

	// Both services render as groups.
	if n := count(`[data-section="changelog-group"]`); n != 2 {
		t.Fatalf("expected 2 changelog service groups, got %d", n)
	}
	// The version + content of an entry render (proves body fetch + tag parse).
	var versionTo, content string
	if err := chromedp.Run(bctx,
		chromedp.Evaluate(`(document.querySelector('[data-service="satellites"] .changelog-version-to')||{}).textContent || ''`, &versionTo),
		chromedp.Evaluate(`(document.querySelector('[data-service="satellites"] [data-field="content"]')||{}).textContent || ''`, &content),
	); err != nil {
		t.Fatalf("read entry: %v", err)
	}
	if versionTo != "0.0.260" {
		t.Errorf("version_to = %q, want 0.0.260", versionTo)
	}
	if content == "" {
		t.Error("changelog entry content did not render (body fetch/markdown failed)")
	}
}
