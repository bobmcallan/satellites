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

// TestConfigPage_SystemDocsRenderAsTableWithExpansion (story_64935bc0)
// covers the V3-style table+expand layout for the /config system
// panels. The panels render scope=system docs catalog-style without
// linking out to /documents/{id}.
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
		if !strings.Contains(panelHTML, `data-table system-doc-table`) {
			t.Errorf("%s: panel missing system-doc-table", tc.label)
		}
		for _, header := range []string{"<th>name</th>", "<th>scope</th>", "<th>status</th>", "<th>category</th>"} {
			if !strings.Contains(panelHTML, header) {
				t.Errorf("%s: panel missing header %q", tc.label, header)
			}
		}
		if strings.Contains(panelHTML, tc.linkSubstring) {
			t.Errorf("%s: panel still contains %q — system docs must not link to /documents/{id}", tc.label, tc.linkSubstring)
		}
		dataRowMarker := `<tr class="system-doc-row" data-testid="config-system-`
		if !strings.Contains(panelHTML, dataRowMarker) {
			t.Errorf("%s: panel data row missing system-doc-row class + data-testid", tc.label)
		}
		if !strings.Contains(panelHTML, `<tr class="system-doc-expansion"`) {
			t.Errorf("%s: panel missing system-doc-expansion row", tc.label)
		}
		if !strings.Contains(panelHTML, `colspan="5"`) {
			t.Errorf("%s: panel expansion row missing colspan=5", tc.label)
		}
		if !strings.Contains(panelHTML, tc.bodyText) {
			t.Errorf("%s: panel missing document body %q", tc.label, tc.bodyText)
		}
		if !strings.Contains(panelHTML, createdLiteral) {
			t.Errorf("%s: panel missing CreatedAt %q", tc.label, createdLiteral)
		}
		if !strings.Contains(panelHTML, updatedLiteral) {
			t.Errorf("%s: panel missing UpdatedAt %q", tc.label, updatedLiteral)
		}
		for _, pillClass := range []string{"category-pill", "scope-pill", "status-pill"} {
			if !strings.Contains(panelHTML, pillClass) {
				t.Errorf("%s: panel missing pill class %q", tc.label, pillClass)
			}
		}
		if !strings.Contains(panelHTML, tc.name) {
			t.Errorf("%s: panel missing document name %q", tc.label, tc.name)
		}
	}

	if createdLiteral == updatedLiteral {
		t.Fatalf("test setup wrong: created == updated, can't distinguish")
	}

	agentSlice := systemPanelSlice("config-system-agents-panel")
	if !strings.Contains(agentSlice, `class="tag-chip pattern-chip"`) {
		t.Errorf("agents panel missing pattern-chip class")
	}
	for _, pattern := range []string{"Read:**", "Edit:**"} {
		if !strings.Contains(agentSlice, pattern) {
			t.Errorf("agents panel missing permission pattern %q", pattern)
		}
	}
}
