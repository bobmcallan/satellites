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
		// story_dda346f9: empty state renders as a compact banner inside
		// a panel-body-compact div, not the full panel-body padding shell.
		`data-testid="config-empty-banner"`,
		`panel-body panel-body-compact`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/config empty body missing %q", want)
		}
	}
	// story_dda346f9 AC1: the empty-state must NOT use the full
	// `<p class="muted">...</p>` + separate `<p>` CTA layout that
	// previously stacked two paragraphs inside the panel body.
	if strings.Contains(body, `<p class="muted" data-testid="config-empty">`) {
		t.Errorf("/config empty body still uses the stacked-paragraph layout — should be the compact banner")
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

// TestConfigPage_SystemDefaultConfigurationRendersInDropdown
// (story_764726d3 AC4) — when configseed has produced the system_default
// scope=system Configuration, the /config page renders it in the
// selector dropdown rather than the empty-state banner.
func TestConfigPage_SystemDefaultConfigurationRendersInDropdown(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Env: "dev", DevMode: true}
	p, users, sessions, _, _, _, _, docs, _ := newTestPortalWithContracts(t, cfg)
	mux := http.NewServeMux()
	p.Register(mux)

	user := auth.User{ID: "u_1", Email: "alice@local"}
	users.Add(user)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	now := time.Now().UTC()
	cfgPayload, err := document.MarshalConfiguration(document.Configuration{
		ContractRefs: []string{}, SkillRefs: []string{}, PrincipleRefs: []string{},
	})
	if err != nil {
		t.Fatalf("marshal Configuration: %v", err)
	}
	systemCfg := document.Document{
		Type:       document.TypeConfiguration,
		Scope:      document.ScopeSystem,
		Name:       "system_default",
		Body:       "system default configuration",
		Status:     document.StatusActive,
		Structured: cfgPayload,
	}
	if _, err := docs.Create(context.Background(), systemCfg, now); err != nil {
		t.Fatalf("seed system_default Configuration: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	body := rec.Body.String()

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, body)
	}
	for _, want := range []string{
		`data-testid="config-selector"`,
		"system_default",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/config body missing %q (system_default Configuration must populate the dropdown)", want)
		}
	}
	if strings.Contains(body, `data-testid="config-empty-banner"`) {
		t.Error("/config still renders the empty banner — system_default should populate the dropdown")
	}
}

// TestConfigPage_SystemDocsRenderAsTableWithExpansion (story_64935bc0
// AC1-5,7-8) — the three system-* panels render as <table class="data-
// table system-doc-table"> with header rows, per-doc <tr class="system-
// doc-row"> data rows, and sibling <tr class="system-doc-expansion">
// rows carrying the body, evidence, chips, and timestamps. Subtle green
// pills (category-pill / scope-pill / status-pill) replace the prior
// stack of <details>. The view-model carries Category for contracts so
// the template doesn't parse Structured JSON in markup.
func TestConfigPage_SystemDocsRenderAsTableWithExpansion(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Env: "dev", DevMode: true}
	p, users, sessions, _, _, _, _, docs, _ := newTestPortalWithContracts(t, cfg)
	mux := http.NewServeMux()
	p.Register(mux)

	user := auth.User{ID: "u_1", Email: "alice@local"}
	users.Add(user)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	createdAt := time.Date(2026, 4, 28, 12, 13, 12, 0, time.UTC)
	updatedAt := time.Date(2026, 4, 29, 9, 30, 0, 0, time.UTC)

	contractStructured := []byte(`{"category":"develop","evidence_required":"build + test","permitted_actions":["Read:**","Edit:**"]}`)
	contractSrc := document.Document{
		Type:       document.TypeContract,
		Scope:      document.ScopeSystem,
		Name:       "develop-system",
		Body:       "develop contract body content",
		Status:     document.StatusActive,
		Structured: contractStructured,
	}
	contractDoc, err := docs.Create(context.Background(), contractSrc, createdAt)
	if err != nil {
		t.Fatalf("seed contract: %v", err)
	}
	if _, err := docs.Update(context.Background(), contractDoc.ID, document.UpdateFields{}, "", updatedAt, nil); err != nil {
		t.Fatalf("bump contract UpdatedAt: %v", err)
	}

	workflowDoc := seedDoc(t, docs, "", document.TypeWorkflow, "default-workflow",
		"default workflow body content", createdAt)
	if _, err := docs.Update(context.Background(), workflowDoc.ID, document.UpdateFields{}, "", updatedAt, nil); err != nil {
		t.Fatalf("bump workflow UpdatedAt: %v", err)
	}

	agentSettings, err := document.MarshalAgentSettings(document.AgentSettings{
		PermissionPatterns: []string{"Read:**", "Edit:**"},
	})
	if err != nil {
		t.Fatalf("marshal agent settings: %v", err)
	}
	agentSrc := document.Document{
		Type:       document.TypeAgent,
		Scope:      document.ScopeSystem,
		Name:       "develop-agent",
		Body:       "develop agent body content",
		Status:     document.StatusActive,
		Structured: agentSettings,
	}
	agentDoc, err := docs.Create(context.Background(), agentSrc, createdAt)
	if err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	if _, err := docs.Update(context.Background(), agentDoc.ID, document.UpdateFields{}, "", updatedAt, nil); err != nil {
		t.Fatalf("bump agent UpdatedAt: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	body := rec.Body.String()

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, body)
	}

	createdLiteral := createdAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	updatedLiteral := updatedAt.UTC().Format("2006-01-02T15:04:05Z07:00")

	// systemPanelSlice returns the HTML between the panel's opening
	// section tag and the next `</section>` so the no-anchor assertion
	// in AC 1-3 is scoped to the system-* panel only — system docs may
	// legitimately appear in the regular `agents` panel below with a
	// link, that's a separate concern.
	systemPanelSlice := func(panelTestID string) string {
		opener := `data-testid="` + panelTestID + `"`
		idx := strings.Index(body, opener)
		if idx < 0 {
			t.Fatalf("panel %q not in body", panelTestID)
		}
		end := strings.Index(body[idx:], `</section>`)
		if end < 0 {
			t.Fatalf("panel %q not closed", panelTestID)
		}
		return body[idx : idx+end]
	}

	for _, tc := range []struct {
		label         string
		panelTestID   string
		id            string
		name          string
		bodyText      string
		linkSubstring string
	}{
		{
			label:         "system contract",
			panelTestID:   "config-system-contracts-panel",
			id:            contractDoc.ID,
			name:          "develop-system",
			bodyText:      "develop contract body content",
			linkSubstring: `href="/documents/` + contractDoc.ID + `"`,
		},
		{
			label:         "system workflow",
			panelTestID:   "config-system-workflows-panel",
			id:            workflowDoc.ID,
			name:          "default-workflow",
			bodyText:      "default workflow body content",
			linkSubstring: `href="/documents/` + workflowDoc.ID + `"`,
		},
		{
			label:         "system agent",
			panelTestID:   "config-system-agents-panel",
			id:            agentDoc.ID,
			name:          "develop-agent",
			bodyText:      "develop agent body content",
			linkSubstring: `href="/documents/` + agentDoc.ID + `"`,
		},
	} {
		panelHTML := systemPanelSlice(tc.panelTestID)
		// AC 1-3: panel uses <table class="data-table system-doc-table">
		// with a header row carrying the column labels; no anchor to
		// /documents/{id} for the seeded doc id.
		if !strings.Contains(panelHTML, `data-table system-doc-table`) {
			t.Errorf("%s: panel missing system-doc-table (AC 1-3 expected V3-style table)", tc.label)
		}
		for _, header := range []string{"<th>name</th>", "<th>scope</th>", "<th>status</th>", "<th>category</th>"} {
			if !strings.Contains(panelHTML, header) {
				t.Errorf("%s: panel missing header %q (AC 1-3)", tc.label, header)
			}
		}
		if strings.Contains(panelHTML, tc.linkSubstring) {
			t.Errorf("%s: panel still contains %q — system docs must not link to /documents/{id} (AC 1-3)", tc.label, tc.linkSubstring)
		}
		// AC 7: data-testid is on the data <tr class="system-doc-row">
		// (not the expansion row).
		dataRowMarker := `<tr class="system-doc-row" data-testid="config-system-` // contract|workflow|agent suffix follows
		if !strings.Contains(panelHTML, dataRowMarker) {
			t.Errorf("%s: panel data row missing system-doc-row class + data-testid (AC 7)", tc.label)
		}
		// AC 4: expansion sub-row present in DOM with colspan="6", carries
		// body + chips + timestamps.
		if !strings.Contains(panelHTML, `<tr class="system-doc-expansion"`) {
			t.Errorf("%s: panel missing system-doc-expansion row (AC 4)", tc.label)
		}
		if !strings.Contains(panelHTML, `colspan="6"`) {
			t.Errorf("%s: panel expansion row missing colspan=6 (AC 4)", tc.label)
		}
		if !strings.Contains(panelHTML, tc.bodyText) {
			t.Errorf("%s: panel missing document body %q (AC 4)", tc.label, tc.bodyText)
		}
		if !strings.Contains(panelHTML, createdLiteral) {
			t.Errorf("%s: panel missing CreatedAt %q (AC 4)", tc.label, createdLiteral)
		}
		if !strings.Contains(panelHTML, updatedLiteral) {
			t.Errorf("%s: panel missing UpdatedAt %q (AC 4)", tc.label, updatedLiteral)
		}
		// AC 5: green pills for category/scope/status. Match the bare
		// class token rather than the full attribute so that combined
		// classes like "status-pill agent-canonical-pill" still satisfy
		// the assertion.
		for _, pillClass := range []string{"category-pill", "scope-pill", "status-pill"} {
			if !strings.Contains(panelHTML, pillClass) {
				t.Errorf("%s: panel missing pill class %q (AC 5)", tc.label, pillClass)
			}
		}
		// AC 4: name appears (in the data row's first cell).
		if !strings.Contains(panelHTML, tc.name) {
			t.Errorf("%s: panel missing document name %q (AC 4)", tc.label, tc.name)
		}
	}

	if createdLiteral == updatedLiteral {
		t.Fatalf("test setup wrong: created == updated, can't distinguish")
	}

	// AC 8: chips render for permitted_actions on the agent expansion row.
	agentSlice := systemPanelSlice("config-system-agents-panel")
	if !strings.Contains(agentSlice, `class="tag-chip pattern-chip"`) {
		t.Errorf("agents panel missing pattern-chip class (AC 8 — chips for permitted_actions)")
	}
	for _, pattern := range []string{"Read:**", "Edit:**"} {
		if !strings.Contains(agentSlice, pattern) {
			t.Errorf("agents panel missing permission pattern %q (AC 8)", pattern)
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
