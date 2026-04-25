package repo

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/ledger"
)

func TestExtractStoryRefs_Empty(t *testing.T) {
	t.Parallel()
	if got := extractStoryRefs("just a normal message"); got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestExtractStoryRefs_Single(t *testing.T) {
	t.Parallel()
	got := extractStoryRefs("feat: thing (story_abcd1234)")
	if len(got) != 1 || got[0] != "story_abcd1234" {
		t.Errorf("got %v, want [story_abcd1234]", got)
	}
}

func TestExtractStoryRefs_HashPrefix(t *testing.T) {
	t.Parallel()
	got := extractStoryRefs("fix #story_abcd1234 something")
	if len(got) != 1 || got[0] != "story_abcd1234" {
		t.Errorf("got %v, want [story_abcd1234]", got)
	}
}

func TestExtractStoryRefs_Multiple(t *testing.T) {
	t.Parallel()
	msg := "feat: cross-cutting story_aaaa1111 and #story_bbbb2222 plus story_aaaa1111 again"
	got := extractStoryRefs(msg)
	if len(got) != 2 {
		t.Fatalf("got %v, want length 2 with dedup", got)
	}
	if got[0] != "story_aaaa1111" || got[1] != "story_bbbb2222" {
		t.Errorf("got %v, want [story_aaaa1111 story_bbbb2222] in first-occurrence order", got)
	}
}

func TestExtractStoryRefs_CaseInsensitive(t *testing.T) {
	t.Parallel()
	got := extractStoryRefs("ref: STORY_ABcd1234")
	if len(got) != 1 || got[0] != "story_abcd1234" {
		t.Errorf("got %v, want [story_abcd1234] (lower-cased)", got)
	}
}

func TestExtractStoryRefs_RejectsShortHex(t *testing.T) {
	t.Parallel()
	if got := extractStoryRefs("story_abc1234"); got != nil {
		t.Errorf("got %v, want nil for 7-char hex", got)
	}
}

func newEmitFixture() (Repo, ledger.Store, time.Time) {
	r := Repo{
		ID:          "repo_1",
		WorkspaceID: "ws_1",
		ProjectID:   "proj_a",
	}
	return r, ledger.NewMemoryStore(), time.Now().UTC()
}

func TestEmitCommitRows_OneCommitOneStory(t *testing.T) {
	t.Parallel()
	r, store, now := newEmitFixture()
	commits := []pushCommit{{
		ID:      "deadbeef00000000000000000000000000000000",
		Message: "feat: thing (story_abcd1234)",
		URL:     "https://example.com/c/deadbeef",
		Author:  pushCommitAuthor{Name: "Alice", Email: "a@x"},
	}}
	emitCommitRows(context.Background(), store, r, commits, now)

	rows, err := store.List(context.Background(), r.ProjectID, ledger.ListOptions{}, nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.Type != ledger.TypeDecision {
		t.Errorf("type = %q, want %q", row.Type, ledger.TypeDecision)
	}
	if row.StoryID == nil || *row.StoryID != "story_abcd1234" {
		t.Errorf("StoryID = %v, want story_abcd1234", row.StoryID)
	}
	if !hasTag(row.Tags, tagCommit) {
		t.Errorf("missing kind:commit tag in %v", row.Tags)
	}
	if !hasTag(row.Tags, "sha:deadbeef00000000000000000000000000000000") {
		t.Errorf("missing sha tag in %v", row.Tags)
	}
	if !hasTag(row.Tags, "story_id:story_abcd1234") {
		t.Errorf("missing story_id tag in %v", row.Tags)
	}
}

func TestEmitCommitRows_OneCommitTwoStories(t *testing.T) {
	t.Parallel()
	r, store, now := newEmitFixture()
	commits := []pushCommit{{
		ID:      "abc123",
		Message: "feat: cross-story (story_aaaa1111, #story_bbbb2222)",
	}}
	emitCommitRows(context.Background(), store, r, commits, now)

	rows, _ := store.List(context.Background(), r.ProjectID, ledger.ListOptions{}, nil)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (one per story)", len(rows))
	}
}

func TestEmitCommitRows_Idempotent(t *testing.T) {
	t.Parallel()
	r, store, now := newEmitFixture()
	commits := []pushCommit{{
		ID:      "abc123",
		Message: "feat: thing (story_abcd1234)",
	}}
	emitCommitRows(context.Background(), store, r, commits, now)
	first, _ := store.List(context.Background(), r.ProjectID, ledger.ListOptions{}, nil)

	emitCommitRows(context.Background(), store, r, commits, now.Add(time.Second))
	second, _ := store.List(context.Background(), r.ProjectID, ledger.ListOptions{}, nil)

	if len(first) != len(second) {
		t.Errorf("idempotent emit added rows: before=%d after=%d", len(first), len(second))
	}
}

func TestEmitCommitRows_NoStoryRefs(t *testing.T) {
	t.Parallel()
	r, store, now := newEmitFixture()
	commits := []pushCommit{{ID: "abc123", Message: "chore: routine maintenance"}}
	emitCommitRows(context.Background(), store, r, commits, now)

	rows, _ := store.List(context.Background(), r.ProjectID, ledger.ListOptions{}, nil)
	if len(rows) != 0 {
		t.Errorf("rows = %d, want 0 for commit with no story refs", len(rows))
	}
}

func TestEmitCommitRows_StructuredPayload(t *testing.T) {
	t.Parallel()
	r, store, now := newEmitFixture()
	commits := []pushCommit{{
		ID:      "deadbeef",
		Message: "feat: thing (story_abcd1234)\n\nlong body that should not appear in subject",
		URL:     "https://example.com/c/deadbeef",
		Author:  pushCommitAuthor{Name: "Alice"},
	}}
	emitCommitRows(context.Background(), store, r, commits, now)

	rows, _ := store.List(context.Background(), r.ProjectID, ledger.ListOptions{}, nil)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	var got commitStructured
	if err := json.Unmarshal(rows[0].Structured, &got); err != nil {
		t.Fatalf("unmarshal structured: %v", err)
	}
	if got.SHA != "deadbeef" {
		t.Errorf("sha = %q", got.SHA)
	}
	if got.Subject != "feat: thing (story_abcd1234)" {
		t.Errorf("subject = %q (should be first line)", got.Subject)
	}
	if got.Author != "Alice" {
		t.Errorf("author = %q", got.Author)
	}
	if got.URL != "https://example.com/c/deadbeef" {
		t.Errorf("url = %q", got.URL)
	}
}

func TestEmitCommitRows_NilStoreNoOp(t *testing.T) {
	t.Parallel()
	emitCommitRows(context.Background(), nil, Repo{}, []pushCommit{{ID: "x", Message: "story_abcd1234"}}, time.Now())
}

func TestEmitCommitRows_EmptySHASkipped(t *testing.T) {
	t.Parallel()
	r, store, now := newEmitFixture()
	commits := []pushCommit{{ID: "  ", Message: "feat: thing (story_abcd1234)"}}
	emitCommitRows(context.Background(), store, r, commits, now)

	rows, _ := store.List(context.Background(), r.ProjectID, ledger.ListOptions{}, nil)
	if len(rows) != 0 {
		t.Errorf("rows = %d, want 0 for empty SHA", len(rows))
	}
}
