package portal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/ledger"
)

func renderLedgerJSON(t *testing.T, p *Portal, projID, sessionCookie, query string) (*httptest.ResponseRecorder, ledgerComposite) {
	t.Helper()
	mux := http.NewServeMux()
	p.Register(mux)
	u := "/projects/" + projID + "/api/ledger"
	if query != "" {
		u += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, u, nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sessionCookie})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var c ledgerComposite
	if rec.Code == http.StatusOK {
		_ = json.Unmarshal(rec.Body.Bytes(), &c)
	}
	return rec, c
}

func TestParseLedgerFilters_QueryString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw  string
		want ledgerFilters
	}{
		{"", ledgerFilters{Tags: []string{}}},
		{"q=foo", ledgerFilters{Query: "foo", Tags: []string{}}},
		{"type=verdict&durability=durable&source_type=agent&status=active",
			ledgerFilters{Type: "verdict", Durability: "durable", SourceType: "agent", Status: "active", Tags: []string{}}},
		{"tag=kind:plan&tag=phase:plan", ledgerFilters{Tags: []string{"kind:plan", "phase:plan"}}},
		{"tag=a,b,c", ledgerFilters{Tags: []string{"a", "b", "c"}}},
		{"story_id=sty_x&contract_id=ci_y", ledgerFilters{StoryID: "sty_x", ContractID: "ci_y", Tags: []string{}}},
	}
	for _, c := range cases {
		u, _ := url.Parse("/x?" + c.raw)
		r := &http.Request{URL: u}
		got := parseLedgerFilters(r)
		// Normalise tag nil vs [].
		if got.Tags == nil {
			got.Tags = []string{}
		}
		if c.want.Tags == nil {
			c.want.Tags = []string{}
		}
		if got.Query != c.want.Query || got.Type != c.want.Type || got.Durability != c.want.Durability ||
			got.SourceType != c.want.SourceType || got.Status != c.want.Status ||
			got.StoryID != c.want.StoryID || got.ContractID != c.want.ContractID ||
			!sameStringSlice(got.Tags, c.want.Tags) {
			t.Errorf("parseLedgerFilters(%q) = %+v, want %+v", c.raw, got, c.want)
		}
	}
}

func sameStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestProjectLedger_HeaderRendersUpgrade(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	p, users, sessions, projects, _, _ := newTestPortal(t, &config.Config{Env: "dev"})
	mux := http.NewServeMux()
	p.Register(mux)

	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	proj, _ := projects.Create(ctx, user.ID, "", "alpha", time.Now().UTC())
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	req := httptest.NewRequest(http.MethodGet, "/projects/"+proj.ID+"/ledger", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`data-testid="ledger-header"`,
		`data-testid="ledger-sidebar"`,
		`data-testid="ledger-search"`,
		`data-testid="tail-toggle"`,
		`data-testid="filter-type"`,
		`data-testid="filter-durability"`,
		`data-testid="filter-source"`,
		`data-testid="filter-status"`,
		`/static/ledger_view.js`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("upgraded ledger body missing %q", want)
		}
	}
}

func TestLedgerJSON_FiltersByTag(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	p, users, sessions, projects, ledgerStore, _ := newTestPortal(t, &config.Config{Env: "dev"})
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	proj, _ := projects.Create(ctx, user.ID, "", "alpha", time.Now().UTC())
	now := time.Now().UTC()
	_, _ = ledgerStore.Append(ctx, ledger.LedgerEntry{
		ProjectID:  proj.ID,
		Type:       ledger.TypeVerdict,
		Tags:       []string{"kind:verdict", "phase:plan"},
		Content:    "approved",
		Durability: ledger.DurabilityDurable,
		SourceType: ledger.SourceSystem,
		Status:     ledger.StatusActive,
	}, now)
	_, _ = ledgerStore.Append(ctx, ledger.LedgerEntry{
		ProjectID:  proj.ID,
		Type:       ledger.TypeArtifact,
		Tags:       []string{"kind:artifact"},
		Content:    "artifact-row",
		Durability: ledger.DurabilityPipeline,
		SourceType: ledger.SourceAgent,
		Status:     ledger.StatusActive,
	}, now.Add(time.Second))
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	rec, c := renderLedgerJSON(t, p, proj.ID, sess.ID, "tag=kind:verdict")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(c.Rows) != 1 {
		t.Fatalf("rows = %d, want 1; got %+v", len(c.Rows), c.Rows)
	}
	if c.Rows[0].Type != ledger.TypeVerdict {
		t.Errorf("row type = %q, want %q", c.Rows[0].Type, ledger.TypeVerdict)
	}
}

func TestLedgerRedirect_NoProjectSendsToList(t *testing.T) {
	t.Parallel()
	p, users, sessions, _, _, _ := newTestPortal(t, &config.Config{Env: "dev"})
	mux := http.NewServeMux()
	p.Register(mux)
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)
	req := httptest.NewRequest(http.MethodGet, "/ledger", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if rec.Header().Get("Location") != "/projects" {
		t.Errorf("Location = %q, want /projects", rec.Header().Get("Location"))
	}
}

func TestLedgerRedirect_PicksFirstProject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	p, users, sessions, projects, _, _ := newTestPortal(t, &config.Config{Env: "dev"})
	mux := http.NewServeMux()
	p.Register(mux)
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	proj, _ := projects.Create(ctx, user.ID, "", "alpha", time.Now().UTC())
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)
	req := httptest.NewRequest(http.MethodGet, "/ledger", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	want := "/projects/" + proj.ID + "/ledger"
	if rec.Header().Get("Location") != want {
		t.Errorf("Location = %q, want %q", rec.Header().Get("Location"), want)
	}
}
