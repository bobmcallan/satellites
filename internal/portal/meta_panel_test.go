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
		`(derived)`,
		wantURL,
		`class="kv-list"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestMetaPanel_MCPRow_EmptyStateWhenPublicURLUnset(t *testing.T) {
	t.Parallel()
	body, _ := renderProjectDetailWithPublic(t, "")
	for _, want := range []string{
		`data-testid="project-meta-mcp"`,
		`data-mcp-state="unset"`,
		"not configured",
		"SATELLITES_PUBLIC_URL",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	// Must NOT emit a half-formed URL when the base is unset.
	for _, mustNot := range []string{
		"data-mcp-derived",
		"/mcp?project_id=",
	} {
		if strings.Contains(body, mustNot) {
			t.Errorf("unset path leaked %q (must not emit half-formed URL)", mustNot)
		}
	}
}
