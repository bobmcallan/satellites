package main

import (
	"context"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
)

// TestSeedLifecycleAgents_CreatesAllSixDocs verifies story_488b8223
// AC1: seedLifecycleAgents creates the six lifecycle agent documents
// each carrying populated permission_patterns.
func TestSeedLifecycleAgents_CreatesAllSixDocs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	docStore := document.NewMemoryStore()

	if err := seedLifecycleAgents(ctx, docStore, "wksp_x", now); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for _, name := range []string{"preplan_agent", "plan_agent", "develop_agent", "push_agent", "merge_agent", "story_close_agent"} {
		d, err := docStore.GetByName(ctx, "", name, nil)
		if err != nil {
			t.Fatalf("%s missing: %v", name, err)
		}
		if d.Type != document.TypeAgent {
			t.Errorf("%s.Type = %q, want %q", name, d.Type, document.TypeAgent)
		}
		settings, err := document.UnmarshalAgentSettings(d.Structured)
		if err != nil {
			t.Errorf("%s settings: %v", name, err)
			continue
		}
		if len(settings.PermissionPatterns) == 0 {
			t.Errorf("%s.PermissionPatterns empty", name)
		}
	}
}

// TestSeedLifecycleAgents_Idempotent verifies AC1 idempotence: a
// second invocation does not duplicate or overwrite existing rows.
func TestSeedLifecycleAgents_Idempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	docStore := document.NewMemoryStore()

	if err := seedLifecycleAgents(ctx, docStore, "wksp_x", now); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	first, err := docStore.GetByName(ctx, "", "develop_agent", nil)
	if err != nil {
		t.Fatalf("develop_agent missing after first seed: %v", err)
	}

	if err := seedLifecycleAgents(ctx, docStore, "wksp_x", now.Add(time.Hour)); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	second, err := docStore.GetByName(ctx, "", "develop_agent", nil)
	if err != nil {
		t.Fatalf("develop_agent missing after second seed: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("ID changed across seeds: %q -> %q (idempotence violated)", first.ID, second.ID)
	}
	if !first.UpdatedAt.Equal(second.UpdatedAt) {
		t.Errorf("UpdatedAt changed across seeds: %v -> %v (overwrite violated)", first.UpdatedAt, second.UpdatedAt)
	}
}

// TestSeedLifecycleAgents_NilStoreShortCircuits guards against panics
// in early-boot code paths that pass a nil store.
func TestSeedLifecycleAgents_NilStoreShortCircuits(t *testing.T) {
	t.Parallel()
	if err := seedLifecycleAgents(context.Background(), nil, "wksp_x", time.Now()); err != nil {
		t.Errorf("nil store: %v (expected nil short-circuit)", err)
	}
}
