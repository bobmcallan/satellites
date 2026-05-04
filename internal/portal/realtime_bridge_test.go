// Regression for sty_af303c26 (focused slice) — the project page must
// expose the data-project-id host attribute that the storyPanel Alpine
// factory reads to scope incoming WS events, plus the WSConfig
// bootstrap that head.html turns into window.SATELLITES_WS. Without
// either, the realtime bridge degrades silently and panels stay stale.
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

func TestProjectDetail_RealtimeBridgeWiring(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Env: "dev", DevMode: true}
	p, users, sessions, projects, _, _, _, workspaces := newTestPortalWithContracts(t, cfg)
	mux := http.NewServeMux()
	p.Register(mux)

	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	now := time.Now().UTC()
	if workspaces != nil {
		ws, _ := workspaces.Create(t.Context(), user.ID, "alpha", now)
		_ = workspaces.AddMember(t.Context(), ws.ID, user.ID, "admin", user.ID, now)
	}
	proj, _ := projects.Create(t.Context(), user.ID, "", "alpha-1", now)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	req := httptest.NewRequest(http.MethodGet, "/projects/"+proj.ID, nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	body := rec.Body.String()

	// data-project-id is what the storyPanel factory reads to scope events.
	if !strings.Contains(body, `data-project-id="`+proj.ID+`"`) {
		t.Errorf("project page missing data-project-id host attribute (sty_af303c26 bridge cannot scope without it)")
	}
}
