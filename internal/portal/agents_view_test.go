// SSR + composite tests for the /agents page upgrades shipped in
// story_7b77ffb0 (portal UI for role-based execution). Cover AC1
// (permission_patterns + skill_refs columns), AC2 (ephemeral flag +
// owning story link + canonical filter), and AC3 (promote-to-canonical
// CTA when a skill set is shared by ≥ promoteCanonicalThreshold
// ephemeral agents).
package portal

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/document"
)

func seedAgent(t *testing.T, docs *document.MemoryStore, name string, settings document.AgentSettings, projectID string, now time.Time) document.Document {
	t.Helper()
	structured, err := document.MarshalAgentSettings(settings)
	if err != nil {
		t.Fatalf("marshal agent settings: %v", err)
	}
	d := document.Document{
		Type:       document.TypeAgent,
		Scope:      document.ScopeSystem,
		Name:       name,
		Body:       "agent body",
		Status:     "active",
		Structured: structured,
	}
	if projectID != "" {
		pid := projectID
		d.ProjectID = &pid
		d.Scope = document.ScopeProject
	}
	out, err := docs.Create(context.Background(), d, now)
	if err != nil {
		t.Fatalf("seed agent %q: %v", name, err)
	}
	return out
}

// TestAgents_RendersPermissionPatternsAndSkills verifies AC1.
func TestAgents_RendersPermissionPatternsAndSkills(t *testing.T) {
	t.Parallel()
	p, users, sessions, _, _, _ := newTestPortal(t, &config.Config{Env: "dev"})
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)
	now := time.Now().UTC()

	docs := p.documents.(*document.MemoryStore)
	skill := seedSkillDoc(t, docs, "golang-testing", now)
	seedAgent(t, docs, "develop_agent", document.AgentSettings{
		PermissionPatterns: []string{"Edit:**", "Bash:go_test"},
		SkillRefs:          []string{skill.ID},
	}, "", now)

	rec := renderPath(t, p, "/agents", sess.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`data-testid="agent-permissions"`,
		`data-testid="agent-skills"`,
		"Edit:**",
		"Bash:go_test",
		skill.ID,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/agents body missing %q", want)
		}
	}
}

// TestAgents_EphemeralFlagAndStoryLink verifies AC2 — ephemeral chip
// + linked story id are rendered.
func TestAgents_EphemeralFlagAndStoryLink(t *testing.T) {
	t.Parallel()
	p, users, sessions, _, _, _ := newTestPortal(t, &config.Config{Env: "dev"})
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)
	now := time.Now().UTC()

	docs := p.documents.(*document.MemoryStore)
	storyID := "story_test_ephemeral"
	seedAgent(t, docs, "ephemeral_one", document.AgentSettings{
		Ephemeral: true,
		StoryID:   &storyID,
	}, "proj_x", now)

	rec := renderPath(t, p, "/agents", sess.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`data-testid="agent-ephemeral"`,
		`agent-ephemeral-pill`,
		"ephemeral",
		storyID,
		`/projects/proj_x/stories/` + storyID,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/agents body missing %q", want)
		}
	}
}

// TestAgents_CanonicalFilter verifies AC2's `?canonical=` filter.
func TestAgents_CanonicalFilter(t *testing.T) {
	t.Parallel()
	p, users, sessions, _, _, _ := newTestPortal(t, &config.Config{Env: "dev"})
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)
	now := time.Now().UTC()

	docs := p.documents.(*document.MemoryStore)
	storyID := "story_test_filter"
	seedAgent(t, docs, "canonical_one", document.AgentSettings{}, "", now)
	seedAgent(t, docs, "ephemeral_one", document.AgentSettings{
		Ephemeral: true,
		StoryID:   &storyID,
	}, "proj_x", now)

	// canonical=true → only canonical_one
	rec := renderPath(t, p, "/agents?canonical=true", sess.ID)
	body := rec.Body.String()
	if !strings.Contains(body, "canonical_one") {
		t.Errorf("canonical filter must include canonical_one; body=%s", body)
	}
	if strings.Contains(body, "ephemeral_one") {
		t.Errorf("canonical filter must EXCLUDE ephemeral_one; body=%s", body)
	}

	// canonical=false → only ephemeral_one
	rec = renderPath(t, p, "/agents?canonical=false", sess.ID)
	body = rec.Body.String()
	if strings.Contains(body, "canonical_one") {
		t.Errorf("ephemeral filter must EXCLUDE canonical_one; body=%s", body)
	}
	if !strings.Contains(body, "ephemeral_one") {
		t.Errorf("ephemeral filter must include ephemeral_one; body=%s", body)
	}

	// no filter → both visible
	rec = renderPath(t, p, "/agents", sess.ID)
	body = rec.Body.String()
	if !strings.Contains(body, "canonical_one") || !strings.Contains(body, "ephemeral_one") {
		t.Errorf("no filter must include both agents; body=%s", body)
	}
}

// TestAgents_PromoteCTA_WhenThresholdMet verifies AC3 — when
// promoteCanonicalThreshold ephemeral agents share a skill set, the
// promote-to-canonical CTA is rendered.
func TestAgents_PromoteCTA_WhenThresholdMet(t *testing.T) {
	t.Parallel()
	p, users, sessions, _, _, _ := newTestPortal(t, &config.Config{Env: "dev"})
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)
	now := time.Now().UTC()

	docs := p.documents.(*document.MemoryStore)
	skill := seedSkillDoc(t, docs, "shared-skill", now)
	storyA := "story_a"
	storyB := "story_b"
	storyC := "story_c"
	seedAgent(t, docs, "ephemeral_a", document.AgentSettings{Ephemeral: true, StoryID: &storyA, SkillRefs: []string{skill.ID}}, "proj_x", now)
	seedAgent(t, docs, "ephemeral_b", document.AgentSettings{Ephemeral: true, StoryID: &storyB, SkillRefs: []string{skill.ID}}, "proj_x", now)
	seedAgent(t, docs, "ephemeral_c", document.AgentSettings{Ephemeral: true, StoryID: &storyC, SkillRefs: []string{skill.ID}}, "proj_x", now)

	rec := renderPath(t, p, "/agents", sess.ID)
	body := rec.Body.String()
	if !strings.Contains(body, `data-testid="agent-promote-cta"`) {
		t.Errorf("promote-CTA must render when 3 ephemeral agents share a skill; body=%s", body)
	}
	// HTML-escaped href: &amp; in DOM body.
	if !strings.Contains(body, "/documents?type=skill&amp;ids="+skill.ID) {
		t.Errorf("CTA href must point at the documents browser pre-filtered to the candidate skill ids; body=%s", body)
	}
}

// TestAgents_PromoteCTA_BelowThreshold verifies the negative case for
// AC3 — two ephemeral agents do NOT trigger the CTA.
func TestAgents_PromoteCTA_BelowThreshold(t *testing.T) {
	t.Parallel()
	p, users, sessions, _, _, _ := newTestPortal(t, &config.Config{Env: "dev"})
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)
	now := time.Now().UTC()

	docs := p.documents.(*document.MemoryStore)
	skill := seedSkillDoc(t, docs, "below-threshold-skill", now)
	storyA := "story_a"
	storyB := "story_b"
	seedAgent(t, docs, "ephemeral_x", document.AgentSettings{Ephemeral: true, StoryID: &storyA, SkillRefs: []string{skill.ID}}, "proj_x", now)
	seedAgent(t, docs, "ephemeral_y", document.AgentSettings{Ephemeral: true, StoryID: &storyB, SkillRefs: []string{skill.ID}}, "proj_x", now)

	rec := renderPath(t, p, "/agents", sess.ID)
	body := rec.Body.String()
	if strings.Contains(body, `data-testid="agent-promote-cta"`) {
		t.Errorf("promote-CTA must NOT render below the threshold; body=%s", body)
	}
}

// TestSummariseEphemeralAgents covers the helper directly so the
// threshold + grouping logic has unit-level coverage.
func TestSummariseEphemeralAgents(t *testing.T) {
	t.Parallel()
	rows := []agentRow{
		{Name: "a", Ephemeral: true, SkillRefs: []string{"s1", "s2"}},
		{Name: "b", Ephemeral: true, SkillRefs: []string{"s2", "s1"}}, // unordered same set
		{Name: "c", Ephemeral: true, SkillRefs: []string{"s1", "s2"}},
		{Name: "d", Ephemeral: true, SkillRefs: []string{"s9"}},
		{Name: "e", Ephemeral: false, SkillRefs: []string{"s1", "s2"}},
	}
	hints := summariseEphemeralAgents(rows)
	if len(hints) != 1 {
		t.Fatalf("hints = %d, want 1; got %+v", len(hints), hints)
	}
	if hints[0].Count != 3 {
		t.Errorf("hint count = %d, want 3", hints[0].Count)
	}
	if !strings.Contains(hints[0].Href, "ids=s1,s2") {
		t.Errorf("hint href = %q, want sorted ids", hints[0].Href)
	}
}

// seedSkillDoc creates a type=skill document with a stub
// contract_binding so document.MemoryStore.Create accepts it. The
// substrate enforces type=skill rows must reference a contract — we
// seed a sibling type=contract doc and bind to it.
func seedSkillDoc(t *testing.T, docs *document.MemoryStore, name string, now time.Time) document.Document {
	t.Helper()
	contract := seedDoc(t, docs, "", document.TypeContract, "for-"+name, "body", now)
	binding := contract.ID
	out, err := docs.Create(context.Background(), document.Document{
		Type:            document.TypeSkill,
		Scope:           document.ScopeSystem,
		Name:            name,
		Body:            "skill body",
		Status:          "active",
		ContractBinding: &binding,
	}, now)
	if err != nil {
		t.Fatalf("seed skill %q: %v", name, err)
	}
	return out
}
