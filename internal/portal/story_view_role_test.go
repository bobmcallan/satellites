// Story-view tests for the role-based execution upgrades originally
// shipped in story_7b77ffb0. The CI-panel-on-story-detail content
// asserted by the prior tests in this file (tree-walk depth, ac_scope
// chip, iteration-warn counter, allocated agent badge) was retired by
// sty_3132035b — those concerns now live on /stories/{id}/walk and are
// covered by story_walk_view_test.go. The ledger-kind-class assertion
// below is the only one that still applies on the story detail page.
package portal

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/story"
)

// TestStoryView_LedgerKindClasses verifies AC8 — distinct ledger
// rows render with the matching kind class.
func TestStoryView_LedgerKindClasses(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, ledgerStore, stories := newTestPortal(t, &config.Config{Env: "dev"})
	ctx := context.Background()
	now := time.Now().UTC()
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	proj, _ := projects.Create(ctx, user.ID, "", "alpha", now)
	s, _ := stories.Create(ctx, story.Story{
		ProjectID: proj.ID, Title: "kind-classes", Status: "in_progress",
		Priority: "high", Category: "feature", CreatedBy: user.ID,
	}, now)
	storyRef := s.ID
	for _, kind := range []string{"plan-amend", "agent-compose", "agent-archive", "session-default-install"} {
		_, err := ledgerStore.Append(ctx, ledger.LedgerEntry{
			ProjectID:  proj.ID,
			StoryID:    &storyRef,
			Type:       ledger.TypeArtifact,
			Tags:       []string{"kind:" + kind},
			Content:    kind + " row",
			Durability: ledger.DurabilityPipeline,
			SourceType: ledger.SourceSystem,
			Status:     ledger.StatusActive,
			CreatedBy:  "system",
		}, now)
		if err != nil {
			t.Fatalf("seed %s row: %v", kind, err)
		}
	}

	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)
	rec := renderStoryDetail(t, p, proj.ID, s.ID, sess.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"ledger-kind-plan-amend",
		"ledger-kind-agent-compose",
		"ledger-kind-agent-archive",
		"ledger-kind-session-default-install",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}
