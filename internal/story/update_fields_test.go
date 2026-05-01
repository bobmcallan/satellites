// Tests for the widened UpdateFields surface (sty_330cc4ab). Confirms
// each pointer field updates in isolation, multiple fields apply
// together, and a non-nil empty Tags slice clears the list.
package story

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/ledger"
)

func ptr[T any](v T) *T { return &v }

func seedUpdateStory(t *testing.T, store *MemoryStore) Story {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	s, err := store.Create(ctx, Story{
		ProjectID:          "proj_a",
		Title:              "original title",
		Description:        "original description",
		AcceptanceCriteria: "original ac",
		Category:           "feature",
		Priority:           "medium",
		Tags:               []string{"epic:original", "ui"},
	}, now)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return s
}

func TestUpdate_TitleOnly(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore(ledger.NewMemoryStore())
	s := seedUpdateStory(t, store)

	got, err := store.Update(context.Background(), s.ID,
		UpdateFields{Title: ptr("new title")},
		"u_alice", time.Now().UTC(), nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Title != "new title" {
		t.Errorf("Title = %q, want %q", got.Title, "new title")
	}
	if got.Description != "original description" {
		t.Errorf("Description should be unchanged, got %q", got.Description)
	}
	if !reflect.DeepEqual(got.Tags, []string{"epic:original", "ui"}) {
		t.Errorf("Tags should be unchanged, got %v", got.Tags)
	}
}

func TestUpdate_AllFieldsTogether(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore(ledger.NewMemoryStore())
	s := seedUpdateStory(t, store)

	got, err := store.Update(context.Background(), s.ID, UpdateFields{
		Title:              ptr("t2"),
		Description:        ptr("d2"),
		AcceptanceCriteria: ptr("ac2"),
		Category:           ptr("bug"),
		Priority:           ptr("high"),
		Tags:               &[]string{"a", "b:c"},
	}, "u_alice", time.Now().UTC(), nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Title != "t2" || got.Description != "d2" || got.AcceptanceCriteria != "ac2" {
		t.Errorf("text fields not all applied: %+v", got)
	}
	if got.Category != "bug" || got.Priority != "high" {
		t.Errorf("enum fields not applied: cat=%q prio=%q", got.Category, got.Priority)
	}
	if !reflect.DeepEqual(got.Tags, []string{"a", "b:c"}) {
		t.Errorf("Tags = %v, want [a b:c]", got.Tags)
	}
}

func TestUpdate_TagsWholesaleReplace(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore(ledger.NewMemoryStore())
	s := seedUpdateStory(t, store)

	got, err := store.Update(context.Background(), s.ID,
		UpdateFields{Tags: &[]string{"only:one"}},
		"u_alice", time.Now().UTC(), nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !reflect.DeepEqual(got.Tags, []string{"only:one"}) {
		t.Errorf("Tags = %v, want exact replacement [only:one] (V3 parity)", got.Tags)
	}
}

func TestUpdate_TagsEmptySliceClears(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore(ledger.NewMemoryStore())
	s := seedUpdateStory(t, store)

	empty := []string{}
	got, err := store.Update(context.Background(), s.ID,
		UpdateFields{Tags: &empty},
		"u_alice", time.Now().UTC(), nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(got.Tags) != 0 {
		t.Errorf("Tags = %v, want [] after clear", got.Tags)
	}
}

func TestUpdate_NoFieldsLeavesContentAlone(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore(ledger.NewMemoryStore())
	s := seedUpdateStory(t, store)

	got, err := store.Update(context.Background(), s.ID,
		UpdateFields{},
		"u_alice", time.Now().UTC(), nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Title != s.Title || got.Description != s.Description {
		t.Errorf("nil fields should not mutate content; got %+v", got)
	}
}
