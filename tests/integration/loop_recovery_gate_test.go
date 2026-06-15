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
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestLoopRecoveryGateEnactment exercises sty_0c98760e AC2/AC5: the
// satellites-loop-recovery-review gate's ENACTMENT — the deterministic plumbing
// behind its {decision,notes} judgment. A non-parent story driven to `blocked`
// by a spent fail loop is recovered to `in_progress` by exactly the two ledger
// rows the gate's "## Enact" section writes on accept (review_accept, then a
// status_transition blocked→in_progress). The status_transition projects onto
// the document status, and the recovered story re-enters with a FRESH quota —
// verb.CountEdgeRejects reads 0 for the loop's state because the exhaustion
// marker already zeroed it.
//
// The gate's LLM judgment is non-deterministic and is unit-covered elsewhere
// (the recovery rationale check); this pins the enactment path and the
// fresh-quota property end-to-end through the real verb + DB projection — the
// integration-tier evidence AC5 calls for.
func TestLoopRecoveryGateEnactment(t *testing.T) {
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
	})

	ctx := context.Background()
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	ws, err := wsStore.Create(ctx, "", "recovery-test-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "recovery-test-pj"}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	createReq, _ := json.Marshal(verb.DocumentUpsertRequest{
		Type:      "story",
		ProjectID: pj.ID,
		Name:      "recovery target",
	})
	raw, err := verb.Dispatch(ctx, "document_upsert", createReq)
	if err != nil {
		t.Fatalf("create story: %v", err)
	}
	var created verb.DocumentUpsertResponse
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatalf("unmarshal create: %v", err)
	}
	storyID := created.Document.ID

	// The state whose fail loop we spend. CountEdgeRejects is keyed by the
	// loop's from_status.
	const loopState = "techdebt-review"

	appendRow := func(kind string, payload map[string]any) {
		p, _ := json.Marshal(payload)
		r, _ := json.Marshal(verb.LedgerAppendRequest{
			StoryID:     storyID,
			ProjectID:   pj.ID,
			WorkspaceID: ws.ID,
			Kind:        kind,
			Payload:     p,
		})
		if _, err := verb.Dispatch(ctx, "ledger_append", r); err != nil {
			t.Fatalf("ledger_append %s: %v", kind, err)
		}
		// Nudge created_at so the oldest-first scan is deterministic.
		time.Sleep(2 * time.Millisecond)
	}

	readStatus := func() string {
		gr, _ := json.Marshal(verb.DocumentGetRequest{ID: storyID})
		raw, err := verb.Dispatch(ctx, "document_get", gr)
		if err != nil {
			t.Fatalf("document_get: %v", err)
		}
		var resp verb.DocumentGetResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal document_get: %v", err)
		}
		return resp.Document.Status
	}

	rejectCount := func() int {
		entries, err := ledStore.ListByStory(ctx, storyID, "")
		if err != nil {
			t.Fatalf("list ledger: %v", err)
		}
		views := make([]verb.LedgerEntryView, 0, len(entries))
		for _, e := range entries {
			views = append(views, verb.LedgerEntryView{Kind: e.Kind, Payload: e.Payload})
		}
		return verb.CountEdgeRejects(views, loopState)
	}

	// Spend the fail loop: three rejects on techdebt-review build the count to
	// its bound...
	for i := 0; i < 3; i++ {
		appendRow("review_reject", map[string]any{
			"from_status": loopState, "gate": "satellites-technical-debt-review",
		})
	}
	if got := rejectCount(); got != 3 {
		t.Fatalf("after 3 rejects, CountEdgeRejects(%s) = %d, want 3", loopState, got)
	}

	// ...then the exhaustion status_transition lands the story in blocked
	// (PlanV2's exhaustion shape: to=on_exhausted, exhausted:true).
	appendRow("status_transition", map[string]any{
		"from_status": loopState, "to_status": "blocked", "exhausted": true,
		"rejects": 3, "max_iterations": 3, "gate": "satellites-technical-debt-review",
	})
	if got := readStatus(); got != "blocked" {
		t.Fatalf("after exhaustion, status = %q, want blocked", got)
	}
	// The exhaustion marker re-armed the loop the moment it fired — the blocked
	// story already carries a fresh quota.
	if got := rejectCount(); got != 0 {
		t.Fatalf("after exhaustion, CountEdgeRejects(%s) = %d, want 0 (re-armed)", loopState, got)
	}

	// Recovery: exactly the two rows the gate writes on accept. The
	// status_transition is what enacts blocked → in_progress.
	appendRow("review_accept", map[string]any{
		"from_status": "blocked", "to_status": "in_progress", "gate": "satellites-loop-recovery-review",
	})
	appendRow("status_transition", map[string]any{
		"from_status": "blocked", "to_status": "in_progress",
	})
	if got := readStatus(); got != "in_progress" {
		t.Fatalf("after recovery, status = %q, want in_progress", got)
	}
	// The recovered story re-enters its review loop at zero, not locked out by
	// the spent loop's rejects.
	if got := rejectCount(); got != 0 {
		t.Fatalf("after recovery, CountEdgeRejects(%s) = %d, want 0 (fresh quota)", loopState, got)
	}
}
