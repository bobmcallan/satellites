// sty_0495f550 — meta panel layout + MCP URL row regression tests.
// AC8 + AC9: assert .kv-list CSS rule ships in portal.css and the
// SSR body renders the mcp row in both configured + unset branches.
package portal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/config"
)

// TestCopyButton_JSHandlerPresent pins the delegated click handler
// for .mcp-copy-btn in common.js. A future refactor that drops the
// handler would silently break the copy affordance; this fails the
// build instead.
func TestCopyButton_JSHandlerPresent(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("../../pages/static/common.js")
	if err != nil {
		t.Fatalf("read common.js: %v", err)
	}
	body := string(src)
	for _, want := range []string{
		"mcp-copy-btn",
		"copyToClipboard",
		"data-copy-source",
		"navigator.clipboard",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("common.js missing %q", want)
		}
	}
}

// TestKVList_CSSRulePresent — AC8: the .kv-list selector ships in
// portal.css with grid layout. The bug was that the class was
// referenced by 9 templates but had zero CSS rules; this test fails
// loudly if a future edit drops the rule again.
func TestKVList_CSSRulePresent(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("../../pages/static/css/portal.css")
	if err != nil {
		t.Fatalf("read portal.css: %v", err)
	}
	body := string(src)
	for _, want := range []string{
		".kv-list {",
		".kv-list dt {",
		".kv-list dd {",
		"display: grid;",
		"text-transform: uppercase;",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("portal.css missing %q — .kv-list cannot lose its layout rule again", want)
		}
	}
}

// renderProjectDetailWithPublic boots a portal with the given
// PublicURL config, signs in alice, creates a project, and returns
// the SSR body for /projects/{id}.
func renderProjectDetailWithPublic(t *testing.T, publicURL string) (string, string) {
	t.Helper()
	cfg := &config.Config{Env: "dev", PublicURL: publicURL}
	p, users, sessions, projects, _, _, _, _, _ := newTestPortalWithContracts(t, cfg)
	mux := http.NewServeMux()
	p.Register(mux)

	alice := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(alice)
	proj, err := projects.Create(context.Background(), alice.ID, "", "alpha", time.Now().UTC())
	if err != nil {
		t.Fatalf("project create: %v", err)
	}
	sess, _ := sessions.Create(alice.ID, auth.DefaultSessionTTL)
	req := httptest.NewRequest(http.MethodGet, "/projects/"+proj.ID, nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String(), proj.ID
}

func TestMetaPanel_MCPRow_DerivedWhenPublicURLSet(t *testing.T) {
	t.Parallel()
	body, projID := renderProjectDetailWithPublic(t, "https://sat.test")
	wantURL := "https://sat.test/mcp?project_id=" + projID
	for _, want := range []string{
		`data-testid="project-meta-mcp"`,
		`data-mcp-derived="true"`,
		`data-mcp-state="derived"`,
		wantURL,
		`class="kv-list"`,
		// Copy button + clipboard plumbing.
		`data-testid="project-meta-mcp-copy"`,
		`class="mcp-copy-btn"`,
		`data-copy-source="` + wantURL + `"`,
		`>[copy]<`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	// "(derived)" suffix dropped per user request.
	if strings.Contains(body, "(derived)") {
		t.Errorf("body still contains \"(derived)\" suffix; should be removed")
	}
}

// V3 parity: when SATELLITES_PUBLIC_URL is unset, the meta panel
// derives the MCP URL from the inbound request's scheme + host. The
// user is already on the portal so the host they reached is by
// definition the right one to paste into .mcp.json.
func TestMetaPanel_MCPRow_DerivesFromRequestWhenPublicURLUnset(t *testing.T) {
	t.Parallel()
	body, projID := renderProjectDetailWithPublic(t, "")
	// httptest.NewRequest defaults to Host=example.com; that's the
	// host the panel must echo back, NOT the not-configured copy.
	wantURL := "http://example.com/mcp?project_id=" + projID
	for _, want := range []string{
		`data-testid="project-meta-mcp"`,
		`data-mcp-derived="true"`,
		wantURL,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	// The unset empty-state copy must not appear when the request has a Host.
	for _, mustNot := range []string{
		`data-mcp-state="unset"`,
		"not configured",
	} {
		if strings.Contains(body, mustNot) {
			t.Errorf("body should not show unset empty-state when request has a host: %q", mustNot)
		}
	}
}
