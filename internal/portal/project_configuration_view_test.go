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

// seedConfigDoc creates a contract or skill document. Skills require a
// contract_binding; the caller passes the contract id (or empty for a
// contract row, which has no binding).
func seedConfigDoc(t *testing.T, docs *document.MemoryStore, projectID, docType, name, binding string, updated time.Time) document.Document {
	t.Helper()
	d := document.Document{
		Type:   docType,
		Name:   name,
		Body:   "x",
		Status: "active",
	}
	if projectID == "" {
		d.Scope = document.ScopeSystem
	} else {
		d.Scope = document.ScopeProject
		pid := projectID
		d.ProjectID = &pid
	}
	if binding != "" {
		b := binding
		d.ContractBinding = &b
	}
	out, err := docs.Create(context.Background(), d, updated)
	if err != nil {
		t.Fatalf("seed %s %q: %v", docType, name, err)
	}
	return out
}

func TestProjectConfiguration_SectionsSplitByType(t *testing.T) {
	t.Parallel()
	docs := document.NewMemoryStore()
	now := time.Now().UTC()

	contract := seedConfigDoc(t, docs, "proj_a", document.TypeContract, "plan", "", now)
	seedConfigDoc(t, docs, "proj_a", document.TypeSkill, "go-style", contract.ID, now)
	// Artifact must NOT appear in either section — sanity check.
	seedDoc(t, docs, "proj_a", document.TypeArtifact, "design", "x", now)

	got := buildProjectConfigurationComposite(context.Background(), docs, "proj_a", nil)

	if len(got.Contracts) != 1 || got.Contracts[0].Name != "plan" {
		t.Errorf("Contracts = %#v, want exactly plan", got.Contracts)
	}
	if len(got.Skills) != 1 || got.Skills[0].Name != "go-style" {
		t.Errorf("Skills = %#v, want exactly go-style", got.Skills)
	}
	for _, c := range got.Contracts {
		if c.Type != document.TypeContract {
			t.Errorf("Contracts contains non-contract type %q", c.Type)
		}
	}
	for _, s := range got.Skills {
		if s.Type != document.TypeSkill {
			t.Errorf("Skills contains non-skill type %q", s.Type)
		}
	}
}

func TestProjectConfiguration_ProjectScoping(t *testing.T) {
	t.Parallel()
	docs := document.NewMemoryStore()
	now := time.Now().UTC()

	seedConfigDoc(t, docs, "proj_a", document.TypeContract, "in-scope", "", now)
	seedConfigDoc(t, docs, "proj_other", document.TypeContract, "out-of-scope", "", now)

	got := buildProjectConfigurationComposite(context.Background(), docs, "proj_a", nil)

	for _, c := range got.Contracts {
		if c.Name == "out-of-scope" {
			t.Errorf("Contracts leaked cross-project doc")
		}
	}
	if len(got.Contracts) != 1 || got.Contracts[0].Name != "in-scope" {
		t.Errorf("Contracts = %#v, want only in-scope", got.Contracts)
	}
}

func TestProjectConfiguration_SystemScopeIncluded(t *testing.T) {
	t.Parallel()
	docs := document.NewMemoryStore()
	now := time.Now().UTC()

	systemContract := seedConfigDoc(t, docs, "", document.TypeContract, "system-plan", "", now)
	seedConfigDoc(t, docs, "proj_a", document.TypeContract, "project-contract", "", now)
	seedConfigDoc(t, docs, "", document.TypeSkill, "system-skill", systemContract.ID, now)

	got := buildProjectConfigurationComposite(context.Background(), docs, "proj_a", nil)

	contractNames := map[string]bool{}
	for _, c := range got.Contracts {
		contractNames[c.Name] = true
	}
	if !contractNames["system-plan"] || !contractNames["project-contract"] {
		t.Errorf("Contracts missing system or project entry: %#v", contractNames)
	}
	skillNames := map[string]bool{}
	for _, s := range got.Skills {
		skillNames[s.Name] = true
	}
	if !skillNames["system-skill"] {
		t.Errorf("Skills missing system entry: %#v", skillNames)
	}
}

func TestProjectConfiguration_DenyAllMembershipsReturnsEmpty(t *testing.T) {
	t.Parallel()
	docs := document.NewMemoryStore()
	now := time.Now().UTC()

	contract := seedConfigDoc(t, docs, "proj_a", document.TypeContract, "plan", "", now)
	seedConfigDoc(t, docs, "proj_a", document.TypeSkill, "go-style", contract.ID, now)

	// Deny-all: empty (non-nil) memberships slice.
	got := buildProjectConfigurationComposite(context.Background(), docs, "proj_a", []string{})

	if len(got.Contracts) != 0 {
		t.Errorf("Contracts under deny-all = %d, want 0", len(got.Contracts))
	}
	if len(got.Skills) != 0 {
		t.Errorf("Skills under deny-all = %d, want 0", len(got.Skills))
	}
}

func TestProjectConfiguration_NilStoreDegrades(t *testing.T) {
	t.Parallel()
	got := buildProjectConfigurationComposite(context.Background(), nil, "proj_a", nil)
	if len(got.Contracts) != 0 || len(got.Skills) != 0 {
		t.Errorf("nil store must yield empty composite, got %#v", got)
	}
}

// renderConfiguration boots a portal, signs in alice, creates her project,
// optionally seeds rows via seedFn, and returns the response from
// GET /projects/{id}/configuration.
func renderConfiguration(t *testing.T, seedFn func(ctx context.Context, projectID string, docs *document.MemoryStore)) *httptest.ResponseRecorder {
	t.Helper()
	p, users, sessions, projects, _, _, _, docs, _ := newTestPortalWithContracts(t, &config.Config{Env: "dev"})
	mux := http.NewServeMux()
	p.Register(mux)

	alice := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(alice)

	ctx := context.Background()
	proj, err := projects.Create(ctx, alice.ID, "", "alpha", time.Now().UTC())
	if err != nil {
		t.Fatalf("project create: %v", err)
	}
	if seedFn != nil {
		seedFn(ctx, proj.ID, docs)
	}

	sess, _ := sessions.Create(alice.ID, auth.DefaultSessionTTL)
	req := httptest.NewRequest(http.MethodGet, "/projects/"+proj.ID+"/configuration", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestProjectConfigurationRender_EmptyShowsPlaceholders(t *testing.T) {
	t.Parallel()
	rec := renderConfiguration(t, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`data-testid="configuration-contracts-panel"`,
		`data-testid="configuration-skills-panel"`,
		`data-testid="configuration-contracts-empty"`,
		`data-testid="configuration-skills-empty"`,
		`No contracts`,
		`No skills`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestProjectConfigurationRender_RowsRenderForSeededRows(t *testing.T) {
	t.Parallel()
	rec := renderConfiguration(t, func(ctx context.Context, projectID string, docs *document.MemoryStore) {
		now := time.Now().UTC()
		pid := projectID
		contract, _ := docs.Create(ctx, document.Document{
			Type: document.TypeContract, Scope: document.ScopeProject, ProjectID: &pid,
			Name: "rendered-contract", Body: "x", Status: "active",
		}, now)
		bind := contract.ID
		_, _ = docs.Create(ctx, document.Document{
			Type: document.TypeSkill, Scope: document.ScopeProject, ProjectID: &pid,
			Name: "rendered-skill", Body: "x", Status: "active", ContractBinding: &bind,
		}, now)
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"rendered-contract",
		"rendered-skill",
		`data-testid="configuration-contracts-list"`,
		`data-testid="configuration-skills-list"`,
		`href="/documents/`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

// TestProjectConfigurationRender_LinkOnProjectDetail asserts that
// project_detail.html (Story 1's surface) now exposes a link to the
// Configuration page so the surface is reachable from the workspace.
func TestProjectConfigurationRender_LinkOnProjectDetail(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, _, _, _, _, _ := newTestPortalWithContracts(t, &config.Config{Env: "dev"})
	mux := http.NewServeMux()
	p.Register(mux)

	alice := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(alice)
	ctx := context.Background()
	proj, _ := projects.Create(ctx, alice.ID, "", "alpha", time.Now().UTC())

	sess, _ := sessions.Create(alice.ID, auth.DefaultSessionTTL)
	req := httptest.NewRequest(http.MethodGet, "/projects/"+proj.ID, nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	wantHref := `href="/projects/` + proj.ID + `/configuration"`
	if !strings.Contains(body, wantHref) {
		t.Errorf("project_detail body missing configuration link %q", wantHref)
	}
	if !strings.Contains(body, `data-testid="panel-configuration-open"`) {
		t.Errorf("project_detail body missing panel-configuration-open testid")
	}
}
