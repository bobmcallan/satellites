//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestStoryRequestReview exercises the full sty_b8c5c23f surface end-to-end:
//
//   - happy path: gate accepts → status flips, ledger captures accept +
//     status_transition rows.
//   - reject path: gate refuses → status unchanged, ledger captures reject.
//   - dynamic path: workflow marks a transition dynamic and the gate
//     picks the next_status itself.
//   - reviewer key lifecycle: mint + revoke ledger rows wrap each
//     dispatch; the minted key is reviewer-role with a TTL.
//   - the executor calling the verb never sees the minted key (it is
//     passed to the dispatcher only, not returned to the verb's caller).
func TestStoryRequestReview(t *testing.T) {
	env := testbootstrap.SetUp(t)
	ctx := context.Background()
	now := time.Now().UTC()

	authStore := auth.New(env.DB)
	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	docStore := document.New(env.DB)
	ledStore := ledger.New(env.DB)

	verb.SetAuthStore(authStore)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	verb.SetDocumentStore(docStore)
	verb.SetLedgerStore(ledStore)
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
		verb.SetDocumentStore(nil)
		verb.SetLedgerStore(nil)
		verb.SetGateDispatcher(nil)
		verb.SetWorkflowSkillReader(nil)
		verb.SetDefaultWorktreeRoot("")
	})

	admin, err := authStore.CreateUser(ctx, "usr_review_admin", "admin@review.test", "Admin", auth.RoleAdmin)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	ws, err := wsStore.Create(ctx, admin.ID, "review-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := wsStore.AddMember(ctx, ws.ID, admin.ID, workspace.RoleAdmin, admin.ID, now); err != nil {
		t.Fatalf("add admin member: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{
		WorkspaceID: ws.ID,
		Name:        "review-pj",
		OwnerUserID: admin.ID,
	}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	adminCtx := auth.WithUser(ctx, admin)

	// Seed the project-config document — the verb reads workflow_skill
	// out of this for every dispatch.
	projectConfigBody := `
story_types:
  feature:
    workflow_skill: .claude/skills/feature-workflow.md
`
	if _, _, err := docStore.Upsert(adminCtx, document.UpsertInput{
		Key: document.Key{
			Scope:       document.ScopeProject,
			WorkspaceID: ws.ID,
			ProjectID:   pj.ID,
			Name:        "project-config",
		},
		Body:      projectConfigBody,
		CreatedBy: admin.ID,
	}, now); err != nil {
		t.Fatalf("seed project-config: %v", err)
	}

	// In-memory workflow-skill content for the declarative scenarios.
	const declarativeSkill = "---\n" +
		"name: feature-workflow\n" +
		"applies_to: [feature]\n" +
		"---\n" +
		"# Feature workflow\n" +
		"\n" +
		"```yaml\n" +
		"states:\n" +
		"  - backlog\n" +
		"  - planning\n" +
		"  - planned\n" +
		"  - in-progress\n" +
		"  - completed\n" +
		"transitions:\n" +
		"  - {from: backlog,     to: planning,    reviewer_skill: \"\"}\n" +
		"  - {from: planning,    to: planned,     reviewer_skill: \"plan-review\"}\n" +
		"  - {from: planned,     to: in-progress, reviewer_skill: \"\"}\n" +
		"  - {from: in-progress, to: completed,   reviewer_skill: \"done-review\"}\n" +
		"```\n"

	// Workflow with a dynamic outgoing edge — exercise the path where
	// the gate skill picks the next_status itself.
	const dynamicSkill = "---\n" +
		"name: dynamic-workflow\n" +
		"applies_to: [feature]\n" +
		"---\n" +
		"# Dynamic workflow\n" +
		"\n" +
		"```yaml\n" +
		"states:\n" +
		"  - triage\n" +
		"  - simple\n" +
		"  - complex\n" +
		"  - completed\n" +
		"transitions:\n" +
		"  - {from: triage,   to: simple,    reviewer_skill: \"triage-gate\", dynamic: true}\n" +
		"  - {from: triage,   to: complex,   reviewer_skill: \"triage-gate\", dynamic: true}\n" +
		"  - {from: simple,   to: completed, reviewer_skill: \"done-review\"}\n" +
		"  - {from: complex,  to: completed, reviewer_skill: \"done-review\"}\n" +
		"```\n"

	skillFiles := map[string]string{
		".claude/skills/feature-workflow.md": declarativeSkill,
	}
	verb.SetWorkflowSkillReader(func(_, relPath string) ([]byte, error) {
		body, ok := skillFiles[relPath]
		if !ok {
			t.Fatalf("unexpected workflow-skill path %q", relPath)
		}
		return []byte(body), nil
	})

	// Helper: create a story in `planning` so the planning→planned
	// reviewer gate is the next outgoing edge.
	mkStory := func(t *testing.T, title string) string {
		t.Helper()
		status := "planning"
		req, _ := json.Marshal(verb.DocumentUpsertRequest{
			Type:      "story",
			ProjectID: pj.ID,
			Name:      title,
			Body:      "story body for " + title,
			Status:    &status,
		})
		raw, err := verb.Dispatch(adminCtx, "document_upsert", req)
		if err != nil {
			t.Fatalf("create story: %v", err)
		}
		var resp verb.DocumentUpsertResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return resp.Document.ID
	}

	t.Run("accept path flips status and writes ledger", func(t *testing.T) {
		storyID := mkStory(t, "accept-story")

		var receivedKey string
		var receivedSkill string
		verb.SetGateDispatcher(verb.GateDispatcherFunc(func(_ context.Context, in verb.GateInput) (verb.GateOutput, error) {
			receivedKey = in.ReviewerKey
			receivedSkill = in.SkillName
			if in.StoryStatus != "planning" {
				t.Fatalf("dispatcher: story_status = %q, want planning", in.StoryStatus)
			}
			if in.NextStatus != "planned" {
				t.Fatalf("dispatcher: next_status = %q, want planned", in.NextStatus)
			}
			return verb.GateOutput{Decision: verb.GateDecisionAccept, Notes: "plan looks good"}, nil
		}))

		reqBody, _ := json.Marshal(verb.StoryRequestReviewRequest{StoryID: storyID})
		raw, err := verb.Dispatch(adminCtx, "story_request_review", reqBody)
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		var resp verb.StoryRequestReviewResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if resp.Decision != verb.GateDecisionAccept {
			t.Fatalf("decision = %q, want accept", resp.Decision)
		}
		if resp.NewStatus != "planned" {
			t.Fatalf("new_status = %q, want planned", resp.NewStatus)
		}
		if receivedSkill != "plan-review" {
			t.Fatalf("dispatcher saw skill %q, want plan-review", receivedSkill)
		}
		if receivedKey == "" {
			t.Fatalf("dispatcher did not receive reviewer key")
		}
		// Caller response carries no reviewer key — executor must not see it.
		rawStr := string(raw)
		if strings.Contains(rawStr, receivedKey) {
			t.Fatalf("verb response leaked reviewer key: %s", rawStr)
		}

		story, _, err := docStore.GetByIDWithLatestBody(ctx, storyID)
		if err != nil {
			t.Fatalf("read story: %v", err)
		}
		if story.Status != "planned" {
			t.Fatalf("story status = %q, want planned", story.Status)
		}

		assertKind(t, ledStore, storyID, verb.KindReviewAccept, 1)
		assertKind(t, ledStore, storyID, verb.KindStatusTransition, 1)
		assertKind(t, ledStore, storyID, ledger.KindReviewerKeyMinted, 1)
		assertKind(t, ledStore, storyID, ledger.KindReviewerKeyRevoked, 1)

		// Once the key has been revoked it must no longer validate.
		if _, err := authStore.ValidateKey(ctx, receivedKey); err == nil {
			t.Fatalf("reviewer key still valid after revoke")
		}
	})

	t.Run("reject path leaves status alone and writes ledger", func(t *testing.T) {
		storyID := mkStory(t, "reject-story")

		verb.SetGateDispatcher(verb.GateDispatcherFunc(func(_ context.Context, _ verb.GateInput) (verb.GateOutput, error) {
			return verb.GateOutput{Decision: verb.GateDecisionReject, Notes: "missing acceptance criteria"}, nil
		}))

		reqBody, _ := json.Marshal(verb.StoryRequestReviewRequest{StoryID: storyID})
		raw, err := verb.Dispatch(adminCtx, "story_request_review", reqBody)
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		var resp verb.StoryRequestReviewResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if resp.Decision != verb.GateDecisionReject {
			t.Fatalf("decision = %q, want reject", resp.Decision)
		}
		if resp.NewStatus != "" {
			t.Fatalf("new_status = %q, want empty on reject", resp.NewStatus)
		}
		if resp.Notes == "" || !strings.Contains(resp.Notes, "acceptance criteria") {
			t.Fatalf("notes = %q, want to surface gate reason", resp.Notes)
		}

		story, _, err := docStore.GetByIDWithLatestBody(ctx, storyID)
		if err != nil {
			t.Fatalf("read story: %v", err)
		}
		if story.Status != "planning" {
			t.Fatalf("story status = %q, want unchanged planning", story.Status)
		}
		assertKind(t, ledStore, storyID, verb.KindReviewReject, 1)
		assertKind(t, ledStore, storyID, verb.KindStatusTransition, 0)
	})

	t.Run("dynamic path lets the gate pick next_status", func(t *testing.T) {
		// Swap in the dynamic workflow + a story sitting at `triage`.
		skillFiles[".claude/skills/feature-workflow.md"] = dynamicSkill
		t.Cleanup(func() {
			skillFiles[".claude/skills/feature-workflow.md"] = declarativeSkill
		})

		status := "triage"
		req, _ := json.Marshal(verb.DocumentUpsertRequest{
			Type:      "story",
			ProjectID: pj.ID,
			Name:      "dynamic-story",
			Status:    &status,
		})
		raw, err := verb.Dispatch(adminCtx, "document_upsert", req)
		if err != nil {
			t.Fatalf("create dynamic story: %v", err)
		}
		var createResp verb.DocumentUpsertResponse
		_ = json.Unmarshal(raw, &createResp)
		storyID := createResp.Document.ID

		verb.SetGateDispatcher(verb.GateDispatcherFunc(func(_ context.Context, in verb.GateInput) (verb.GateOutput, error) {
			if !in.Dynamic {
				t.Fatalf("dynamic flag did not propagate to dispatcher")
			}
			return verb.GateOutput{Decision: verb.GateDecisionAccept, NextStatus: "complex"}, nil
		}))

		reqBody, _ := json.Marshal(verb.StoryRequestReviewRequest{StoryID: storyID})
		raw, err = verb.Dispatch(adminCtx, "story_request_review", reqBody)
		if err != nil {
			t.Fatalf("dispatch dynamic: %v", err)
		}
		var resp verb.StoryRequestReviewResponse
		_ = json.Unmarshal(raw, &resp)
		if resp.NewStatus != "complex" {
			t.Fatalf("dynamic new_status = %q, want complex", resp.NewStatus)
		}
	})

	t.Run("reviewer key TTL caps the mint", func(t *testing.T) {
		// Mint a reviewer key directly to verify the TTL surface from
		// sty_25d5b21e is intact — story_request_review reuses this path.
		rawKey, key, err := authStore.IssueReviewerKey(ctx, auth.IssueReviewerKeyInput{
			UserID:    admin.ID,
			ProjectID: pj.ID,
			AgentName: "ttl-probe",
			TTL:       50 * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("mint reviewer key: %v", err)
		}
		if key.ExpiresAt == nil {
			t.Fatalf("reviewer key has no expires_at")
		}
		if key.Role != auth.APIKeyRoleReviewer {
			t.Fatalf("role = %q, want reviewer", key.Role)
		}
		if _, err := authStore.ValidateKey(ctx, rawKey); err != nil {
			t.Fatalf("fresh key should validate: %v", err)
		}
		time.Sleep(80 * time.Millisecond)
		if _, err := authStore.ValidateKey(ctx, rawKey); err == nil {
			t.Fatalf("expired key should not validate")
		}
	})

	t.Run("rejects when project-config has no entry for story_type", func(t *testing.T) {
		// Empty story_types mapping forces the lookup to miss.
		if _, _, err := docStore.Upsert(adminCtx, document.UpsertInput{
			Key: document.Key{
				Scope:       document.ScopeProject,
				WorkspaceID: ws.ID,
				ProjectID:   pj.ID,
				Name:        "project-config",
			},
			Body:      "story_types: {}\n",
			CreatedBy: admin.ID,
		}, now); err != nil {
			t.Fatalf("rewrite project-config: %v", err)
		}
		t.Cleanup(func() {
			_, _, _ = docStore.Upsert(adminCtx, document.UpsertInput{
				Key: document.Key{
					Scope:       document.ScopeProject,
					WorkspaceID: ws.ID,
					ProjectID:   pj.ID,
					Name:        "project-config",
				},
				Body:      projectConfigBody,
				CreatedBy: admin.ID,
			}, now)
		})

		storyID := mkStory(t, "no-config-story")
		reqBody, _ := json.Marshal(verb.StoryRequestReviewRequest{StoryID: storyID})
		_, err := verb.Dispatch(adminCtx, "story_request_review", reqBody)
		if err == nil || !strings.Contains(err.Error(), "story_types") && !strings.Contains(err.Error(), "workflow_skill") {
			t.Fatalf("expected workflow_skill lookup failure, got %v", err)
		}
	})
}

func assertKind(t *testing.T, store *ledger.Store, storyID, kind string, want int) {
	t.Helper()
	entries, err := store.List(context.Background(), storyID, kind)
	if err != nil {
		t.Fatalf("ledger list %s: %v", kind, err)
	}
	if len(entries) != want {
		t.Fatalf("ledger kind=%s count = %d, want %d", kind, len(entries), want)
	}
}
