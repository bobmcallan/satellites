package portal

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/config"
)

// TestPortal_Footer_Renders covers AC2/AC3: the footer partial appears on
// the dashboard, the unauth landing, /projects, and a project-scoped page.
// Story_1340913b.
func TestPortal_Footer_Renders(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Env: "dev", DevMode: true}
	p, users, sessions, projects, _, _ := newTestPortal(t, cfg)
	mux := http.NewServeMux()
	p.Register(mux)

	user := auth.User{ID: "u_1", Email: "alice@local"}
	users.Add(user)
	proj, err := projects.Create(testCtx(), user.ID, "alpha", "", time.Now().UTC())
	if err != nil {
		t.Fatalf("project create: %v", err)
	}
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	cases := []struct {
		name string
		path string
		auth bool
	}{
		{name: "auth dashboard", path: "/", auth: true},
		{name: "unauth landing", path: "/", auth: false},
		{name: "projects list", path: "/projects", auth: true},
		{name: "project detail", path: "/projects/" + proj.ID, auth: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.auth {
				req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sess.ID})
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			body := rec.Body.String()
			if !strings.Contains(body, `<footer class="footer"`) {
				t.Errorf("missing <footer class=\"footer\"> on %s — body[len=%d] tail=%q", tc.path, len(body), tailSuffix(body, 200))
			}
			if !strings.Contains(body, "SATELLITES") {
				t.Errorf("footer brand absent on %s", tc.path)
			}
		})
	}
}

// TestPortal_Footer_OmitsCommitWhenUnknown covers AC4: when commit is
// empty or "unknown" the footer renders only the version, with no
// `·` separator. story_1340913b.
func TestPortal_Footer_OmitsCommitWhenUnknown(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Env: "dev", DevMode: true}
	p, users, sessions, _, _, _ := newTestPortal(t, cfg)
	mux := http.NewServeMux()
	p.Register(mux)

	user := auth.User{ID: "u_1", Email: "alice@local"}
	users.Add(user)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	body := rec.Body.String()

	footerStart := strings.Index(body, `<footer class="footer"`)
	if footerStart < 0 {
		t.Fatalf("footer missing")
	}
	footer := body[footerStart:]
	footerEnd := strings.Index(footer, `</footer>`)
	if footerEnd < 0 {
		t.Fatalf("</footer> missing")
	}
	footer = footer[:footerEnd]

	// The default Commit at compile time is "unknown" (config.GitCommit).
	// The footer template must therefore omit both the separator and the
	// commit sha.
	if strings.Contains(footer, "&middot;") || strings.Contains(footer, "·") {
		t.Errorf("footer contains separator when commit=unknown:\n%s", footer)
	}
	if strings.Contains(footer, "unknown") {
		t.Errorf("footer leaked the literal commit \"unknown\":\n%s", footer)
	}
}

// TestPortal_Footer_ThreeSlotShape (sty_558c0431) — confirms the
// rendered footer carries the three slots (identity / uptime / status)
// and the data-testids the chromedp + DOM tests pivot on.
func TestPortal_Footer_ThreeSlotShape(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Env: "dev", DevMode: true}
	p, users, sessions, _, _, _ := newTestPortal(t, cfg)
	mux := http.NewServeMux()
	p.Register(mux)

	user := auth.User{ID: "u_1", Email: "alice@local"}
	users.Add(user)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	body := rec.Body.String()

	for _, want := range []string{
		`x-data="footerStatus"`,
		`data-testid="footer-identity"`,
		`data-testid="footer-uptime"`,
		`data-testid="footer-status"`,
		`x-text="uptimeLabel"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("footer body missing %q", want)
		}
	}
}

// tailSuffix returns the last n characters of s for diagnostic snippets.
func tailSuffix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
