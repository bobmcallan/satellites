package portal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/document"
)

// TestConfigPage_EmptyState (story_644a2eb1 AC6) — when the workspace
// has no Configuration documents, /config renders the empty-state copy
// and a CTA link.
func TestConfigPage_EmptyState(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Env: "dev", DevMode: true}
	p, users, sessions, _, _, _ := newTestPortal(t, cfg)
	mux := http.NewServeMux()
	p.Register(mux)

	user := auth.User{ID: "u_1", Email: "alice@local"}
	users.Add(user)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	body := rec.Body.String()

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, body)
	}
	for _, want := range []string{
		`data-testid="config-empty"`,
		`data-testid="config-empty-cta"`,
		`No Configurations yet`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/config empty body missing %q", want)
		}
	}
}

// seedConfiguration writes a type=configuration document with the supplied
// refs into the in-memory document store.
func seedConfiguration(t *testing.T, docs *document.MemoryStore, projectID, name string, refs document.Configuration, updated time.Time) document.Document {
	t.Helper()
	payload, err := document.MarshalConfiguration(refs)
	if err != nil {
		t.Fatalf("marshal config %q: %v", name, err)
	}
	d := document.Document{
		Type:       document.TypeConfiguration,
		Scope:      document.ScopeProject,
		Name:       name,
		Body:       "",
		Status:     "active",
		Structured: payload,
	}
	pid := projectID
	d.ProjectID = &pid
	out, err := docs.Create(context.Background(), d, updated)
	if err != nil {
		t.Fatalf("seed config %q: %v", name, err)
	}
	return out
}

// TestConfigPage_DropdownListsConfigurations (story_644a2eb1 AC3) —
// every Configuration document in the workspace appears as an option in
// the dropdown.
func TestConfigPage_DropdownListsConfigurations(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Env: "dev", DevMode: true}
	p, users, sessions, _, _, _, _, docs, _ := newTestPortalWithContracts(t, cfg)
	mux := http.NewServeMux()
	p.Register(mux)

	user := auth.User{ID: "u_1", Email: "alice@local"}
	users.Add(user)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	now := time.Now().UTC()
	cfgA := seedConfiguration(t, docs, "proj_x", "alpha bundle", document.Configuration{}, now.Add(-time.Hour))
	cfgB := seedConfiguration(t, docs, "proj_x", "beta bundle", document.Configuration{}, now)

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	body := rec.Body.String()

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	for _, want := range []string{
		`data-testid="config-selector"`,
		cfgA.ID,
		cfgB.ID,
		"alpha bundle",
		"beta bundle",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/config dropdown body missing %q", want)
		}
	}
}

// TestConfigPage_SectionsRenderRefs (story_644a2eb1 AC2+AC4) — selecting
// a Configuration via ?id renders the workflow / contracts / skills /
// principles sections populated with the resolved doc names.
func TestConfigPage_SectionsRenderRefs(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Env: "dev", DevMode: true}
	p, users, sessions, _, _, _, _, docs, _ := newTestPortalWithContracts(t, cfg)
	mux := http.NewServeMux()
	p.Register(mux)

	user := auth.User{ID: "u_1", Email: "alice@local"}
	users.Add(user)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	now := time.Now().UTC()
	contractDoc := seedDoc(t, docs, "", document.TypeContract, "develop", "body", now)
	binding := contractDoc.ID
	skillSrc := document.Document{
		Type:            document.TypeSkill,
		Scope:           document.ScopeSystem,
		Name:            "golang-testing",
		Body:            "body",
		Status:          "active",
		ContractBinding: &binding,
	}
	skillDoc, err := docs.Create(context.Background(), skillSrc, now)
	if err != nil {
		t.Fatalf("seed skill: %v", err)
	}
	principleDoc := seedDoc(t, docs, "", document.TypePrinciple, "pr-quality", "body", now)
	cfgRefs := document.Configuration{
		ContractRefs:  []string{contractDoc.ID},
		SkillRefs:     []string{skillDoc.ID},
		PrincipleRefs: []string{principleDoc.ID},
	}
	cfgDoc := seedConfiguration(t, docs, "proj_x", "primary bundle", cfgRefs, now)

	req := httptest.NewRequest(http.MethodGet, "/config?id="+cfgDoc.ID, nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	body := rec.Body.String()

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, body)
	}
	for _, want := range []string{
		`data-testid="config-workflow-panel"`,
		`data-testid="config-contracts-panel"`,
		`data-testid="config-skills-panel"`,
		`data-testid="config-principles-panel"`,
		"develop",
		"golang-testing",
		"pr-quality",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/config sections body missing %q", want)
		}
	}

	// Selected option renders with `selected` attribute.
	if !strings.Contains(body, `value="`+cfgDoc.ID+`" selected`) {
		t.Errorf("dropdown should mark %q as selected; body=%s", cfgDoc.ID, body)
	}
}
