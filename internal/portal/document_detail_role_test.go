// Tests for the document_detail.html upgrades shipped in
// story_7b77ffb0: type=agent panel renders permission_patterns,
// skill_refs, ephemeral/canonical status, owning story link (AC11).
//
// Also verifies AC11 part 1: type=contract documents do NOT render a
// permitted_actions panel — confirmed via repo-grep at preplan time
// (no panel exists). This test asserts the negative explicitly.
package portal

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/document"
)

// TestDocumentDetail_AgentRendersPermissionsAndSkills verifies the
// type=agent panel renders the permission_patterns + skill_refs.
func TestDocumentDetail_AgentRendersPermissionsAndSkills(t *testing.T) {
	t.Parallel()
	p, users, sessions, _, _, _ := newTestPortal(t, &config.Config{Env: "dev"})
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)
	now := time.Now().UTC()

	docs := p.documents.(*document.MemoryStore)
	skill := seedSkillDoc(t, docs, "agent-skill", now)
	agent := seedAgent(t, docs, "developer_agent", document.AgentSettings{
		PermissionPatterns: []string{"Edit:**", "Bash:go_test"},
		SkillRefs:          []string{skill.ID},
	}, "", now)

	rec := renderPath(t, p, "/documents/"+agent.ID, sess.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`data-testid="document-agent-panel"`,
		`data-testid="document-agent-permissions"`,
		`data-testid="document-agent-skills"`,
		"Edit:**",
		"Bash:go_test",
		skill.ID,
		`data-testid="document-agent-kind"`,
		"canonical",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

// TestDocumentDetail_AgentEphemeralLink verifies that ephemeral
// agents render the owning-story link.
func TestDocumentDetail_AgentEphemeralLink(t *testing.T) {
	t.Parallel()
	p, users, sessions, _, _, _ := newTestPortal(t, &config.Config{Env: "dev"})
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)
	now := time.Now().UTC()

	docs := p.documents.(*document.MemoryStore)
	storyID := "story_xyz"
	agent := seedAgent(t, docs, "ephemeral_one", document.AgentSettings{
		Ephemeral: true,
		StoryID:   &storyID,
	}, "proj_x", now)

	rec := renderPath(t, p, "/documents/"+agent.ID, sess.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`data-testid="document-agent-kind"`,
		"ephemeral",
		`data-testid="document-agent-story"`,
		storyID,
		`/projects/proj_x/stories/` + storyID,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

// TestDocumentDetail_ContractNoPermittedActions verifies AC11 part 1
// — type=contract document detail does NOT render a permitted_actions
// panel.
func TestDocumentDetail_ContractNoPermittedActions(t *testing.T) {
	t.Parallel()
	p, users, sessions, _, _, _ := newTestPortal(t, &config.Config{Env: "dev"})
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)
	now := time.Now().UTC()

	docs := p.documents.(*document.MemoryStore)
	c := seedDoc(t, docs, "", document.TypeContract, "develop", "body", now)

	rec := renderPath(t, p, "/documents/"+c.ID, sess.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, banned := range []string{
		"permitted_actions",
		"permitted-actions",
		"PermittedActions",
	} {
		if strings.Contains(body, banned) {
			t.Errorf("type=contract detail must NOT render %q (story_b39b393f removed the field)", banned)
		}
	}
}
