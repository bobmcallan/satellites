package agentprocess

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
)

// TestSystemDefaultBody_PinsContractTokens is the regression test
// that AC6 of sty_e1ab884d demands. The seeded body MUST keep its
// fundamentals + routing tokens; a future edit that drops any of
// them breaks this test loudly.
func TestSystemDefaultBody_PinsContractTokens(t *testing.T) {
	t.Parallel()
	mustContain := []string{
		// Fundamentals.
		"configuration over code",
		"story is the unit",
		"workflow is a list of contract",
		"process order",
		"session = one role",
		"five primitives",
		// Routing rules.
		"satellites_project_set",
		"satellites_story_get",
		"implement <story_id>",
	}
	for _, want := range mustContain {
		if !strings.Contains(SystemDefaultBody, want) {
			t.Errorf("SystemDefaultBody missing required token %q — the seed is the contract; do not drop tokens", want)
		}
	}
}

func TestSeedSystemDefault_Idempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := document.NewMemoryStore()
	now := time.Now().UTC()

	if err := SeedSystemDefault(ctx, store, "wksp_a", now); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	if err := SeedSystemDefault(ctx, store, "wksp_a", now.Add(time.Minute)); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	rows, err := store.List(ctx, document.ListOptions{
		Type:  document.TypeArtifact,
		Scope: document.ScopeSystem,
		Tags:  []string{KindTag},
	}, nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	matched := 0
	for _, r := range rows {
		if r.Name == SystemDefaultName {
			matched++
		}
	}
	if matched != 1 {
		t.Errorf("seeded default_agent_process count = %d, want 1 (idempotent re-seed must not duplicate)", matched)
	}
}

func TestResolve_ProjectOverrideWins(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := document.NewMemoryStore()
	now := time.Now().UTC()

	if err := SeedSystemDefault(ctx, store, "wksp_a", now); err != nil {
		t.Fatalf("seed default: %v", err)
	}

	pid := "proj_alpha"
	if _, err := store.Create(ctx, document.Document{
		WorkspaceID: "wksp_a",
		ProjectID:   &pid,
		Type:        document.TypeArtifact,
		Scope:       document.ScopeProject,
		Name:        ProjectOverrideName,
		Body:        "PROJECT_OVERRIDE_BODY",
		Status:      document.StatusActive,
		Tags:        []string{KindTag},
	}, now); err != nil {
		t.Fatalf("seed override: %v", err)
	}
	body := Resolve(ctx, store, pid, nil)
	if body != "PROJECT_OVERRIDE_BODY" {
		t.Errorf("Resolve(project=set) = %q, want PROJECT_OVERRIDE_BODY", body)
	}
}

func TestResolve_FallsBackToSystemDefault(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := document.NewMemoryStore()
	now := time.Now().UTC()
	if err := SeedSystemDefault(ctx, store, "wksp_a", now); err != nil {
		t.Fatalf("seed: %v", err)
	}
	body := Resolve(ctx, store, "proj_no_override", nil)
	if !strings.Contains(body, "configuration over code") {
		t.Errorf("Resolve fell through to empty when system default should serve")
	}
}

func TestResolve_EmptyWhenNeitherSeeded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := document.NewMemoryStore()
	body := Resolve(ctx, store, "proj_anything", nil)
	if body != "" {
		t.Errorf("Resolve with no seeds = %q, want empty", body)
	}
}

func TestResolve_NilStore(t *testing.T) {
	t.Parallel()
	if got := Resolve(context.Background(), nil, "proj", nil); got != "" {
		t.Errorf("Resolve(nil store) = %q, want empty", got)
	}
}

func TestResolve_DenyAllMembershipsReturnsEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := document.NewMemoryStore()
	now := time.Now().UTC()
	_ = SeedSystemDefault(ctx, store, "wksp_a", now)
	if got := Resolve(ctx, store, "proj_x", []string{}); got != "" {
		t.Errorf("Resolve(deny-all memberships) = %q, want empty", got)
	}
}

func TestResolve_RejectsWrongTypeOrInactive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := document.NewMemoryStore()
	now := time.Now().UTC()
	// Right name, but type=principle — must not be returned.
	if _, err := store.Create(ctx, document.Document{
		WorkspaceID: "wksp_a",
		ProjectID:   nil,
		Type:        document.TypePrinciple,
		Scope:       document.ScopeSystem,
		Name:        SystemDefaultName,
		Body:        "WRONG_TYPE",
		Status:      document.StatusActive,
		Tags:        []string{KindTag},
	}, now); err != nil {
		t.Fatalf("seed wrong-type: %v", err)
	}
	if got := Resolve(ctx, store, "", nil); got == "WRONG_TYPE" {
		t.Errorf("Resolve served a non-artifact row with the right name")
	}
}
