package portal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/story"
)

// seedActivityLedger writes one ledger row per (kind, phase, content,
// dt-offset) tuple under the given storyID + projectID so the test can
// assert filter + ordering behaviour without piping through the
// substrate's claim/close handlers.
func seedActivityLedger(t *testing.T, store ledger.Store, projectID, storyID string, base time.Time, rows []seedRow) []ledger.LedgerEntry {
	t.Helper()
	out := make([]ledger.LedgerEntry, 0, len(rows))
	storyRef := storyID
	for i, r := range rows {
		entry, err := store.Append(context.Background(), ledger.LedgerEntry{
			ProjectID:  projectID,
			StoryID:    &storyRef,
			Type:       ledger.TypeDecision,
			Tags:       r.tags,
			Content:    r.content,
			Durability: ledger.DurabilityDurable,
			SourceType: ledger.SourceAgent,
			Status:     ledger.StatusActive,
			CreatedBy:  "u_alice",
		}, base.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatalf("seed row %d: %v", i, err)
		}
		out = append(out, entry)
	}
	return out
}

type seedRow struct {
	tags    []string
	content string
}

func TestBuildStoryActivity_FilterAndOrdering(t *testing.T) {
	t.Parallel()
	store := ledger.NewMemoryStore()
	storyID := "sty_test_e55f"
	projID := "proj_test"
	base := time.Date(2026, 5, 2, 6, 0, 0, 0, time.UTC)
	seedActivityLedger(t, store, projID, storyID, base, []seedRow{
		{tags: []string{"kind:plan", "phase:orchestrator"}, content: "plan composed"},
		{tags: []string{"kind:noise"}, content: "should be filtered out"},
		{tags: []string{"kind:action-claim", "phase:plan"}, content: "plan CI claimed"},
		{tags: []string{"kind:close-request", "phase:plan"}, content: "plan CI close requested"},
		{tags: []string{"kind:verdict", "phase:plan"}, content: "verdict approved"},
	})

	rows := buildStoryActivity(context.Background(), store, projID, storyID, DefaultStoryActivityKinds, nil)
	if len(rows) != 4 {
		t.Fatalf("filtered rows = %d, want 4 (excluding kind:noise); rows = %+v", len(rows), rows)
	}
	wantOrder := []string{"kind:plan", "kind:action-claim", "kind:close-request", "kind:verdict"}
	for i, want := range wantOrder {
		if rows[i].Kind != want {
			t.Errorf("row[%d].Kind = %q, want %q (oldest-first ordering broken)", i, rows[i].Kind, want)
		}
	}
	for _, row := range rows {
		if strings.HasPrefix(row.KindClass, "kind:") {
			t.Errorf("KindClass leaks prefix: %q", row.KindClass)
		}
	}
	actionClaim := rows[1]
	if actionClaim.Kind != "kind:action-claim" {
		t.Fatalf("unexpected ordering: %+v", actionClaim)
	}
	if actionClaim.Phase != "plan" {
		t.Errorf("Phase = %q, want %q", actionClaim.Phase, "plan")
	}
}

func TestBuildStoryActivity_EmptyStoryReturnsEmpty(t *testing.T) {
	t.Parallel()
	store := ledger.NewMemoryStore()
	rows := buildStoryActivity(context.Background(), store, "proj_empty", "sty_empty", DefaultStoryActivityKinds, nil)
	if rows == nil {
		t.Fatalf("rows is nil; want empty slice for clean empty-state JSON")
	}
	if len(rows) != 0 {
		t.Errorf("rows = %+v, want empty", rows)
	}
}

func TestBuildStoryActivity_KVOverride(t *testing.T) {
	t.Parallel()
	store := ledger.NewMemoryStore()
	storyID := "sty_kv_test"
	projID := "proj_kv"
	base := time.Date(2026, 5, 2, 6, 0, 0, 0, time.UTC)

	// Seed three activity rows under the default set, then a KV
	// override that narrows the filter to just `kind:plan`.
	seedActivityLedger(t, store, projID, storyID, base, []seedRow{
		{tags: []string{"kind:plan", "phase:orchestrator"}, content: "plan"},
		{tags: []string{"kind:agent-compose"}, content: "agent composed"},
		{tags: []string{"kind:verdict", "phase:plan"}, content: "verdict"},
	})

	// Project-scope KV override to `[kind:plan]` only.
	overrideAt := base.Add(time.Hour)
	_, err := store.Append(context.Background(), ledger.LedgerEntry{
		ProjectID:  projID,
		Type:       ledger.TypeKV,
		Tags:       []string{"key:" + StoryActivityKVKey, "scope:project"},
		Content:    `["kind:plan"]`,
		Durability: ledger.DurabilityDurable,
		SourceType: ledger.SourceUser,
		Status:     ledger.StatusActive,
		CreatedBy:  "u_alice",
	}, overrideAt)
	if err != nil {
		t.Fatalf("seed kv override: %v", err)
	}

	resolved := resolveStoryActivityKinds(context.Background(), store, "", projID, nil)
	if len(resolved) != 1 || resolved[0] != "kind:plan" {
		t.Fatalf("resolved kinds = %+v, want [kind:plan]", resolved)
	}
	rows := buildStoryActivity(context.Background(), store, projID, storyID, resolved, nil)
	if len(rows) != 1 {
		t.Fatalf("filtered rows = %d, want 1 under KV override; rows = %+v", len(rows), rows)
	}
	if rows[0].Kind != "kind:plan" {
		t.Errorf("filtered kind = %q, want kind:plan", rows[0].Kind)
	}
}

func TestParseActivityKindsJSON_AcceptsBothFormats(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"json array", `["kind:plan","kind:verdict"]`, []string{"kind:plan", "kind:verdict"}},
		{"comma list", "kind:plan, kind:verdict", []string{"kind:plan", "kind:verdict"}},
		{"prefix added", "plan,verdict", []string{"kind:plan", "kind:verdict"}},
		{"malformed json", `[broken`, nil},
	}
	for _, c := range cases {
		got := parseActivityKindsJSON(c.in)
		if len(got) != len(c.want) {
			t.Errorf("%s: parsed %+v, want %+v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: parsed[%d] = %q, want %q", c.name, i, got[i], c.want[i])
			}
		}
	}
}

func TestIsStoryActivityKindTagged(t *testing.T) {
	t.Parallel()
	if !IsStoryActivityKindTagged([]string{"kind:plan", "phase:orchestrator"}, nil) {
		t.Errorf("default set: kind:plan should match")
	}
	if IsStoryActivityKindTagged([]string{"kind:noise"}, nil) {
		t.Errorf("default set: kind:noise should NOT match")
	}
	if !IsStoryActivityKindTagged([]string{"kind:custom"}, []string{"kind:custom"}) {
		t.Errorf("explicit set: kind:custom should match when in set")
	}
	if IsStoryActivityKindTagged(nil, nil) {
		t.Errorf("empty tags should not match anything")
	}
}

func TestStoryActivityEndpoint_ReturnsBackfill(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, ledgerStore, stories := newTestPortal(t, &config.Config{Env: "dev"})
	ctx := context.Background()
	now := time.Now().UTC()
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	proj, _ := projects.Create(ctx, user.ID, "", "alpha", now)
	s, _ := stories.Create(ctx, story.Story{
		ProjectID: proj.ID,
		Title:     "with activity",
		Status:    "in_progress",
		Priority:  "high",
		Category:  "feature",
		CreatedBy: user.ID,
	}, now)
	storyRef := s.ID
	for i, kind := range []string{"kind:plan", "kind:agent-compose", "kind:action-claim"} {
		_, err := ledgerStore.Append(ctx, ledger.LedgerEntry{
			ProjectID:  proj.ID,
			StoryID:    &storyRef,
			Type:       ledger.TypeDecision,
			Tags:       []string{kind},
			Content:    kind + " content",
			Durability: ledger.DurabilityDurable,
			SourceType: ledger.SourceAgent,
			Status:     ledger.StatusActive,
			CreatedBy:  "u_alice",
		}, now.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatalf("seed ledger row %d: %v", i, err)
		}
	}
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	mux := http.NewServeMux()
	p.Register(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/stories/"+s.ID+"/activity", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q", rec.Header().Get("Content-Type"))
	}
	var body storyActivityComposite
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.StoryID != s.ID {
		t.Errorf("story_id = %q, want %q", body.StoryID, s.ID)
	}
	if len(body.Rows) != 3 {
		t.Errorf("rows = %d, want 3", len(body.Rows))
	}
	wantKinds := []string{"kind:plan", "kind:agent-compose", "kind:action-claim"}
	for i, want := range wantKinds {
		if body.Rows[i].Kind != want {
			t.Errorf("row[%d].kind = %q, want %q", i, body.Rows[i].Kind, want)
		}
	}
	if len(body.Kinds) == 0 {
		t.Errorf("kinds list should be non-empty (default set)")
	}
}

func TestStoryActivityEndpoint_CrossOwner404(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, _, stories := newTestPortal(t, &config.Config{Env: "dev"})
	ctx := context.Background()
	now := time.Now().UTC()
	alice := auth.User{ID: "u_alice", Email: "alice@local"}
	bob := auth.User{ID: "u_bob", Email: "bob@local"}
	users.Add(alice)
	users.Add(bob)
	proj, _ := projects.Create(ctx, alice.ID, "", "alice-only", now)
	s, _ := stories.Create(ctx, story.Story{
		ProjectID: proj.ID,
		Title:     "alice-only-activity",
		Status:    "in_progress",
		Priority:  "high",
		Category:  "feature",
		CreatedBy: alice.ID,
	}, now)
	bobSess, _ := sessions.Create(bob.ID, auth.DefaultSessionTTL)
	mux := http.NewServeMux()
	p.Register(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/stories/"+s.ID+"/activity", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: bobSess.ID})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (no leak across owners)", rec.Code)
	}
}

func TestStoryDetail_RendersActivityPanel(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, ledgerStore, stories := newTestPortal(t, &config.Config{Env: "dev"})
	ctx := context.Background()
	now := time.Now().UTC()
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	proj, _ := projects.Create(ctx, user.ID, "", "alpha", now)
	s, _ := stories.Create(ctx, story.Story{
		ProjectID: proj.ID,
		Title:     "panel-render",
		Status:    "in_progress",
		Priority:  "high",
		Category:  "feature",
		CreatedBy: user.ID,
	}, now)
	storyRef := s.ID
	_, _ = ledgerStore.Append(ctx, ledger.LedgerEntry{
		ProjectID:  proj.ID,
		StoryID:    &storyRef,
		Type:       ledger.TypeDecision,
		Tags:       []string{"kind:plan", "phase:orchestrator"},
		Content:    "orchestrator plan composed",
		Durability: ledger.DurabilityDurable,
		SourceType: ledger.SourceAgent,
		Status:     ledger.StatusActive,
		CreatedBy:  user.ID,
	}, now)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)
	rec := renderStoryDetail(t, p, proj.ID, s.ID, sess.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// Panel testid present
	if !strings.Contains(body, `data-testid="story-activity-log"`) {
		t.Errorf("body missing data-testid=\"story-activity-log\"")
	}
	// SSR row present (no empty-state SSR marker)
	if strings.Contains(body, `data-testid="story-activity-empty-ssr"`) {
		t.Errorf("body should not show empty-state SSR marker when activity rows exist")
	}
	if !strings.Contains(body, `kind:plan`) {
		t.Errorf("body missing kind:plan tag in activity row")
	}
	if !strings.Contains(body, "orchestrator plan composed") {
		t.Errorf("body missing activity summary")
	}
}

func TestStoryDetail_EmptyActivityRendersEmptyState(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, _, stories := newTestPortal(t, &config.Config{Env: "dev"})
	ctx := context.Background()
	now := time.Now().UTC()
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	proj, _ := projects.Create(ctx, user.ID, "", "alpha", now)
	s, _ := stories.Create(ctx, story.Story{
		ProjectID: proj.ID,
		Title:     "empty-activity",
		Status:    "backlog",
		Priority:  "high",
		Category:  "feature",
		CreatedBy: user.ID,
	}, now)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)
	rec := renderStoryDetail(t, p, proj.ID, s.ID, sess.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-testid="story-activity-empty-ssr"`) {
		t.Errorf("body missing empty-state SSR marker for zero-activity story")
	}
}
