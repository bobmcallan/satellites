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

// TestEngagementTickerDedupAndPrefixList pins the server half of the
// engagement ticker (sty_84c55d0d):
//   - ledger_append of an engagement:* row is idempotent on
//     (session, story, seq) — a replay returns deduplicated without a second row
//   - a different seq appends normally
//   - ledger_list kind_prefix returns the engagement family across kinds
func TestEngagementTickerDedupAndPrefixList(t *testing.T) {
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
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	ws, err := wsStore.Create(ctx, "", "ticker-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "ticker-pj"}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	storyReq, _ := json.Marshal(verb.DocumentUpsertRequest{Type: "story", ProjectID: pj.ID, Name: "ticker story"})
	rawStory, err := verb.Dispatch(ctx, "document_upsert", storyReq)
	if err != nil {
		t.Fatalf("create story: %v", err)
	}
	var created verb.DocumentUpsertResponse
	_ = json.Unmarshal(rawStory, &created)
	story := created.Document.ID

	appendTick := func(kind string, seq int64) map[string]any {
		payload, _ := json.Marshal(map[string]any{"phase": "in_progress", "seq": seq, "lease_until": "2026-06-12T12:00:00Z"})
		req, _ := json.Marshal(verb.LedgerAppendRequest{
			StoryID: story, ProjectID: pj.ID, SessionID: "sess-1",
			Kind: kind, Body: "in_progress", Payload: payload,
		})
		raw, err := verb.Dispatch(ctx, "ledger_append", req)
		if err != nil {
			t.Fatalf("ledger_append %s seq=%d: %v", kind, seq, err)
		}
		var resp map[string]any
		_ = json.Unmarshal(raw, &resp)
		return resp
	}

	countEngagement := func() int {
		req, _ := json.Marshal(verb.LedgerListRequest{StoryID: story, KindPrefix: "engagement:"})
		raw, err := verb.Dispatch(ctx, "ledger_list", req)
		if err != nil {
			t.Fatalf("ledger_list: %v", err)
		}
		var resp verb.LedgerListResponse
		_ = json.Unmarshal(raw, &resp)
		return len(resp.Entries)
	}

	appendTick("engagement:engage", 1)
	if got := countEngagement(); got != 1 {
		t.Fatalf("after first append: %d rows, want 1", got)
	}

	// Replay the same (session, story, seq) — idempotent, no second row.
	resp := appendTick("engagement:engage", 1)
	if dedup, _ := resp["deduplicated"].(bool); !dedup {
		t.Errorf("replay must report deduplicated, got %v", resp)
	}
	if got := countEngagement(); got != 1 {
		t.Fatalf("after replay: %d rows, want 1 (seq-keyed dedup)", got)
	}

	// A different seq (and kind in the same family) appends.
	appendTick("engagement:tick", 2)
	if got := countEngagement(); got != 2 {
		t.Fatalf("after second seq: %d rows, want 2", got)
	}

	// kind_prefix sees the family; an unrelated kind is excluded.
	otherReq, _ := json.Marshal(verb.LedgerAppendRequest{StoryID: story, ProjectID: pj.ID, SessionID: "sess-1", Kind: "step_summary", Body: "x"})
	if _, err := verb.Dispatch(ctx, "ledger_append", otherReq); err != nil {
		t.Fatalf("append other kind: %v", err)
	}
	if got := countEngagement(); got != 2 {
		t.Fatalf("kind_prefix must exclude non-engagement kinds: got %d, want 2", got)
	}
}
