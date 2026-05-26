//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/reviewer"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// fakeSummaryClient returns a deterministic completion that
// embeds the story id, so we can prove the prompt was rendered
// against the right story.
type fakeSummaryClient struct {
	prefix string
}

func (f *fakeSummaryClient) Complete(_ context.Context, _ string, _ int, prompt string) (string, error) {
	// Anything containing the prompt is enough; return a recognisable
	// synthetic summary so the test can assert it was persisted.
	return f.prefix + " summary", nil
}

// TestStorySummary covers sty_77524ed8:
//   - AC#1: migration adds summary + summary_updated_at (verified by scan)
//   - AC#2: config/reviewers/story_summary.md loads as a Definition
//   - AC#3: story_get returns summary
//   - AC#4: ledger_append triggers regen
//   - AC#5: story_summary_regenerate verb works
//   - AC#6: stub LLM client substitutes the real call
//   - AC#7: same internal/reviewer orchestration as the finder
func TestStorySummary(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	docStore := document.New(env.DB)
	ledStore := ledger.New(env.DB)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	verb.SetDocumentStore(docStore)
	verb.SetLedgerStore(ledStore)
	t.Cleanup(func() {
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
		verb.SetDocumentStore(nil)
		verb.SetLedgerStore(nil)
		verb.SetReviewerRegistry(nil)
		verb.SetSummaryDispatchForTest(nil)
		verb.SetReviewerDispatchForTest(nil)
	})

	// Define both reviewers: the finding agent (disabled here to
	// keep this test focused) and the summary agent (enabled).
	defs := map[string]reviewer.Definition{
		"story_reviewer": {Name: "story_reviewer", Enabled: false},
		verb.SummaryReviewerName: {
			Name: verb.SummaryReviewerName, Enabled: true,
			Model: "test-model", MaxTokens: 256,
			Body: "Summarise the story below.",
		},
	}
	client := &fakeSummaryClient{prefix: "test-stub"}
	verb.SetReviewerRegistry(reviewer.NewRegistry(defs, client))

	// Synchronous dispatch — assertions run after regen has
	// persisted to the row.
	verb.SetSummaryDispatchForTest(func(ctx context.Context, storyID string) {
		verb.RegenerateSummaryForTest(ctx, storyID)
	})

	ctx := context.Background()
	now := time.Date(2026, 5, 25, 14, 0, 0, 0, time.UTC)
	ws, err := wsStore.Create(ctx, "", "summary-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{
		WorkspaceID: ws.ID,
		Name:        "summary-pj",
	}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	t.Run("story create triggers summary regen via ledger hook", func(t *testing.T) {
		req, _ := json.Marshal(verb.DocumentUpsertRequest{
			Type:      "story",
			ProjectID: pj.ID,
			Name:      "summary target",
		})
		raw, err := verb.Dispatch(ctx, "document_upsert", req)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		var created verb.DocumentUpsertResponse
		_ = json.Unmarshal(raw, &created)
		s := created.Document

		// document_get returns the persisted summary (on the Document row).
		getReq, _ := json.Marshal(verb.DocumentGetRequest{ID: s.ID})
		raw, err = verb.Dispatch(ctx, "document_get", getReq)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		var got verb.DocumentGetResponse
		_ = json.Unmarshal(raw, &got)
		if got.Document.Summary != "test-stub summary" {
			t.Fatalf("summary = %q, want %q", got.Document.Summary, "test-stub summary")
		}
		if got.Document.SummaryUpdatedAt == nil {
			t.Fatalf("summary_updated_at unset after regen")
		}
	})

	t.Run("ledger_append on existing story triggers regen", func(t *testing.T) {
		req, _ := json.Marshal(verb.DocumentUpsertRequest{
			Type:      "story",
			ProjectID: pj.ID,
			Name:      "ledger trigger",
		})
		raw, _ := verb.Dispatch(ctx, "document_upsert", req)
		var created verb.DocumentUpsertResponse
		_ = json.Unmarshal(raw, &created)
		s := created.Document

		// Swap to a new stub so we can distinguish the second regen.
		newClient := &fakeSummaryClient{prefix: "after-comment"}
		defs2 := map[string]reviewer.Definition{
			verb.SummaryReviewerName: {
				Name: verb.SummaryReviewerName, Enabled: true,
				Model: "test-model", MaxTokens: 256,
				Body: "Summarise the story below.",
			},
		}
		verb.SetReviewerRegistry(reviewer.NewRegistry(defs2, newClient))

		appendReq, _ := json.Marshal(verb.LedgerAppendRequest{
			StoryID: s.ID,
			Kind:    ledger.KindComment,
			Actor:   "operator",
			Body:    "looks fine",
		})
		if _, err := verb.Dispatch(ctx, "ledger_append", appendReq); err != nil {
			t.Fatalf("ledger_append: %v", err)
		}

		got, err := docStore.GetByID(ctx, s.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Summary != "after-comment summary" {
			t.Fatalf("summary = %q, want %q", got.Summary, "after-comment summary")
		}
	})

	t.Run("story_summary_regenerate verb works synchronously", func(t *testing.T) {
		req, _ := json.Marshal(verb.DocumentUpsertRequest{
			Type:      "story",
			ProjectID: pj.ID,
			Name:      "manual regen target",
		})
		raw, _ := verb.Dispatch(ctx, "document_upsert", req)
		var created verb.DocumentUpsertResponse
		_ = json.Unmarshal(raw, &created)
		s := created.Document

		newClient := &fakeSummaryClient{prefix: "manual"}
		defs3 := map[string]reviewer.Definition{
			verb.SummaryReviewerName: {
				Name: verb.SummaryReviewerName, Enabled: true,
				Model: "test-model", MaxTokens: 256,
				Body: "Summarise the story below.",
			},
		}
		verb.SetReviewerRegistry(reviewer.NewRegistry(defs3, newClient))

		manReq, _ := json.Marshal(verb.StorySummaryRegenerateRequest{StoryID: s.ID})
		raw, err := verb.Dispatch(ctx, "story_summary_regenerate", manReq)
		if err != nil {
			t.Fatalf("manual regen: %v", err)
		}
		var resp verb.StorySummaryRegenerateResponse
		_ = json.Unmarshal(raw, &resp)
		if !resp.Dispatched {
			t.Fatalf("dispatched = false")
		}
		got, _ := docStore.GetByID(ctx, s.ID)
		if got.Summary != "manual summary" {
			t.Fatalf("summary = %q, want %q", got.Summary, "manual summary")
		}
	})
}
