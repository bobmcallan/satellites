// Story-view tests for the role-based execution upgrades shipped in
// story_7b77ffb0: tree-walk render with depth (AC5), ac_scope chip
// (AC6), iteration counter with warn class above cap/2 (AC7), distinct
// CSS class for plan-amend / agent-compose / agent-archive /
// session-default-install ledger rows (AC8), allocated agent link on
// each CI (AC9).
package portal

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/story"
)

// seedAgentForStoryTest creates a type=agent doc the story-view tests
// link CIs to.
func seedAgentForStoryTest(t *testing.T, docs *document.MemoryStore, name string, now time.Time) document.Document {
	t.Helper()
	return seedAgent(t, docs, name, document.AgentSettings{}, "", now)
}

// TestStoryView_TreeOrderRendersChildIndented verifies AC5 — child CIs
// (those with a non-nil parent_invocation_id) render after their parent
// and carry a non-zero depth.
func TestStoryView_TreeOrderRendersChildIndented(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, _, stories, contracts, docs, _ := newTestPortalWithContracts(t, &config.Config{Env: "dev"})
	ctx := context.Background()
	now := time.Now().UTC()
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	proj, _ := projects.Create(ctx, user.ID, "", "alpha", now)
	s, _ := stories.Create(ctx, story.Story{
		ProjectID: proj.ID,
		Title:     "tree-walk fixture",
		Status:    "in_progress",
		Priority:  "high",
		Category:  "feature",
		CreatedBy: user.ID,
	}, now)

	contractDoc := seedDoc(t, docs, "", document.TypeContract, "develop", "body", now)
	parent, err := contracts.Create(ctx, contract.ContractInstance{
		StoryID:      s.ID,
		ContractID:   contractDoc.ID,
		ContractName: "develop",
		Sequence:     2,
		Status:       contract.StatusPassed,
	}, now)
	if err != nil {
		t.Fatalf("seed parent CI: %v", err)
	}
	child, err := contracts.Create(ctx, contract.ContractInstance{
		StoryID:            s.ID,
		ContractID:         contractDoc.ID,
		ContractName:       "develop",
		Sequence:           3,
		Status:             contract.StatusReady,
		ParentInvocationID: parent.ID,
	}, now.Add(time.Second))
	if err != nil {
		t.Fatalf("seed child CI: %v", err)
	}

	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)
	rec := renderStoryDetail(t, p, proj.ID, s.ID, sess.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	parentIdx := strings.Index(body, `data-ci-id="`+parent.ID+`"`)
	childIdx := strings.Index(body, `data-ci-id="`+child.ID+`"`)
	if parentIdx < 0 || childIdx < 0 {
		t.Fatalf("parent or child row missing; body=%s", body)
	}
	if childIdx < parentIdx {
		t.Errorf("child CI must render AFTER parent (parentIdx=%d, childIdx=%d)", parentIdx, childIdx)
	}
	if !strings.Contains(body, `data-parent="`+parent.ID+`"`) {
		t.Errorf("child row must carry data-parent referencing the parent")
	}
	if !strings.Contains(body, `ci-depth-1`) {
		t.Errorf("child row must carry ci-depth-1 class")
	}
}

// TestStoryView_ACScopeChip verifies AC6 — each CI carries an
// ac-scope chip whose text reflects ACScope.
func TestStoryView_ACScopeChip(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, _, stories, contracts, docs, _ := newTestPortalWithContracts(t, &config.Config{Env: "dev"})
	ctx := context.Background()
	now := time.Now().UTC()
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	proj, _ := projects.Create(ctx, user.ID, "", "alpha", now)
	s, _ := stories.Create(ctx, story.Story{
		ProjectID: proj.ID, Title: "ac-scope", Status: "in_progress",
		Priority: "high", Category: "feature", CreatedBy: user.ID,
	}, now)
	contractDoc := seedDoc(t, docs, "", document.TypeContract, "develop", "body", now)
	full, err := contracts.Create(ctx, contract.ContractInstance{
		StoryID: s.ID, ContractID: contractDoc.ID, ContractName: "develop",
		Sequence: 1, Status: contract.StatusReady, ACScope: []int{1, 2, 3, 4, 5},
	}, now)
	if err != nil {
		t.Fatalf("seed full CI: %v", err)
	}
	scoped, err := contracts.Create(ctx, contract.ContractInstance{
		StoryID: s.ID, ContractID: contractDoc.ID, ContractName: "develop",
		Sequence: 2, Status: contract.StatusReady, ACScope: []int{2},
	}, now)
	if err != nil {
		t.Fatalf("seed scoped CI: %v", err)
	}
	_ = full
	_ = scoped

	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)
	rec := renderStoryDetail(t, p, proj.ID, s.ID, sess.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`data-testid="ci-ac-scope"`,
		"AC 1..5",
		"AC 2",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

// TestStoryView_IterationCounter_WarnAboveHalfCap verifies AC7. The
// default cap is 5; the third CI covering AC 2 surfaces a 3/5
// iteration with the warn class.
func TestStoryView_IterationCounter_WarnAboveHalfCap(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, _, stories, contracts, docs, _ := newTestPortalWithContracts(t, &config.Config{Env: "dev"})
	ctx := context.Background()
	now := time.Now().UTC()
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	proj, _ := projects.Create(ctx, user.ID, "", "alpha", now)
	s, _ := stories.Create(ctx, story.Story{
		ProjectID: proj.ID, Title: "iteration", Status: "in_progress",
		Priority: "high", Category: "feature", CreatedBy: user.ID,
	}, now)
	contractDoc := seedDoc(t, docs, "", document.TypeContract, "develop", "body", now)
	for i := 1; i <= 3; i++ {
		_, err := contracts.Create(ctx, contract.ContractInstance{
			StoryID: s.ID, ContractID: contractDoc.ID, ContractName: "develop",
			Sequence: i, Status: contract.StatusReady, ACScope: []int{2},
		}, now.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatalf("seed CI %d: %v", i, err)
		}
	}

	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)
	rec := renderStoryDetail(t, p, proj.ID, s.ID, sess.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-testid="ci-iteration-counter"`) {
		t.Errorf("iteration counter chip missing")
	}
	if !strings.Contains(body, `iteration-warn`) {
		t.Errorf("iteration-warn class missing for the third re-scope of AC 2")
	}
	if !strings.Contains(body, "3/5 iterations") {
		t.Errorf("iteration text 3/5 iterations missing; body=%s", body)
	}
}

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

// TestStoryView_AgentLink verifies AC9 — each CI renders the allocated
// agent's link when AgentID is non-empty.
func TestStoryView_AgentLink(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, _, stories, contracts, docs, _ := newTestPortalWithContracts(t, &config.Config{Env: "dev"})
	ctx := context.Background()
	now := time.Now().UTC()
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	proj, _ := projects.Create(ctx, user.ID, "", "alpha", now)
	s, _ := stories.Create(ctx, story.Story{
		ProjectID: proj.ID, Title: "agent-link", Status: "in_progress",
		Priority: "high", Category: "feature", CreatedBy: user.ID,
	}, now)
	agent := seedAgentForStoryTest(t, docs, "developer_agent", now)
	contractDoc := seedDoc(t, docs, "", document.TypeContract, "develop", "body", now)
	allocated, err := contracts.Create(ctx, contract.ContractInstance{
		StoryID: s.ID, ContractID: contractDoc.ID, ContractName: "develop",
		Sequence: 1, Status: contract.StatusReady, AgentID: agent.ID,
	}, now)
	if err != nil {
		t.Fatalf("seed allocated CI: %v", err)
	}
	unallocated, err := contracts.Create(ctx, contract.ContractInstance{
		StoryID: s.ID, ContractID: contractDoc.ID, ContractName: "develop",
		Sequence: 2, Status: contract.StatusReady,
	}, now.Add(time.Second))
	if err != nil {
		t.Fatalf("seed unallocated CI: %v", err)
	}

	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)
	rec := renderStoryDetail(t, p, proj.ID, s.ID, sess.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-testid="ci-agent" data-agent-id="`+agent.ID+`"`) {
		t.Errorf("allocated CI must render ci-agent with agent_id; body=%s", body)
	}
	if !strings.Contains(body, "developer_agent") {
		t.Errorf("allocated CI must render the resolved agent name; body=%s", body)
	}
	_ = allocated
	_ = unallocated
}

// ctx is a tiny context.TODO helper kept local to this file so the
// test stays self-contained.
func ctx(t *testing.T) context.Context {
	t.Helper()
	return context.Background()
}
