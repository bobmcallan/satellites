package ledger

import (
	"context"
	"testing"
	"time"
)

func TestKVProjection_LatestPerKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	t0 := time.Now().UTC()

	_, _ = store.Append(ctx, LedgerEntry{ProjectID: "proj_a", Type: TypeKV, Tags: []string{"key:active_branch"}, Content: "main"}, t0)
	_, _ = store.Append(ctx, LedgerEntry{ProjectID: "proj_a", Type: TypeKV, Tags: []string{"key:active_branch"}, Content: "feature/x"}, t0.Add(time.Hour))
	_, _ = store.Append(ctx, LedgerEntry{ProjectID: "proj_a", Type: TypeKV, Tags: []string{"key:max_retries"}, Content: "5"}, t0.Add(2*time.Hour))

	kv, err := KVProjection(ctx, store, "proj_a", nil)
	if err != nil {
		t.Fatalf("KVProjection: %v", err)
	}
	if len(kv) != 2 {
		t.Fatalf("len = %d, want 2", len(kv))
	}
	if kv["active_branch"].Value != "feature/x" {
		t.Errorf("active_branch = %q, want feature/x (newest wins)", kv["active_branch"].Value)
	}
	if kv["active_branch"].Scope != KVScopeProject {
		t.Errorf("legacy row Scope = %q, want %q", kv["active_branch"].Scope, KVScopeProject)
	}
	if kv["max_retries"].Value != "5" {
		t.Errorf("max_retries = %q, want 5", kv["max_retries"].Value)
	}
}

func TestKVProjectionScoped_System(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	t0 := time.Now().UTC()

	// System row: empty workspace_id, scope:system tag.
	_, _ = store.Append(ctx, LedgerEntry{Type: TypeKV, Tags: []string{"scope:system", "key:default_theme"}, Content: "dark"}, t0)
	// A workspace row that should NOT bleed into the system projection.
	_, _ = store.Append(ctx, LedgerEntry{WorkspaceID: "ws_1", Type: TypeKV, Tags: []string{"scope:workspace", "key:default_theme"}, Content: "blue"}, t0.Add(time.Hour))

	kv, err := KVProjectionScoped(ctx, store, KVProjectionOptions{Scope: KVScopeSystem}, nil)
	if err != nil {
		t.Fatalf("KVProjectionScoped: %v", err)
	}
	if got, want := len(kv), 1; got != want {
		t.Fatalf("system kv len = %d, want %d", got, want)
	}
	if v := kv["default_theme"].Value; v != "dark" {
		t.Errorf("system default_theme = %q, want %q", v, "dark")
	}
	if s := kv["default_theme"].Scope; s != KVScopeSystem {
		t.Errorf("system Scope = %q, want %q", s, KVScopeSystem)
	}
}

func TestKVProjectionScoped_Workspace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	t0 := time.Now().UTC()

	// Two workspaces, same key in each.
	_, _ = store.Append(ctx, LedgerEntry{WorkspaceID: "ws_1", Type: TypeKV, Tags: []string{"scope:workspace", "key:tier"}, Content: "gold"}, t0)
	_, _ = store.Append(ctx, LedgerEntry{WorkspaceID: "ws_2", Type: TypeKV, Tags: []string{"scope:workspace", "key:tier"}, Content: "silver"}, t0)
	// A project row in ws_1 — must NOT bleed into the workspace projection.
	_, _ = store.Append(ctx, LedgerEntry{WorkspaceID: "ws_1", ProjectID: "proj_a", Type: TypeKV, Tags: []string{"scope:project", "key:tier"}, Content: "platinum"}, t0)

	kv, err := KVProjectionScoped(ctx, store, KVProjectionOptions{Scope: KVScopeWorkspace, WorkspaceID: "ws_1"}, []string{"ws_1"})
	if err != nil {
		t.Fatalf("KVProjectionScoped: %v", err)
	}
	if got, want := len(kv), 1; got != want {
		t.Fatalf("ws_1 kv len = %d, want %d", got, want)
	}
	if v := kv["tier"].Value; v != "gold" {
		t.Errorf("ws_1 tier = %q, want %q (project row leaked or wrong workspace)", v, "gold")
	}
}

func TestKVProjectionScoped_Project(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	t0 := time.Now().UTC()

	_, _ = store.Append(ctx, LedgerEntry{WorkspaceID: "ws_1", ProjectID: "proj_a", Type: TypeKV, Tags: []string{"scope:project", "key:max_retries"}, Content: "5"}, t0)
	_, _ = store.Append(ctx, LedgerEntry{WorkspaceID: "ws_1", ProjectID: "proj_a", Type: TypeKV, Tags: []string{"scope:project", "key:max_retries"}, Content: "10"}, t0.Add(time.Hour))
	// A workspace-scope row that must NOT show up in the project projection.
	_, _ = store.Append(ctx, LedgerEntry{WorkspaceID: "ws_1", Type: TypeKV, Tags: []string{"scope:workspace", "key:max_retries"}, Content: "1"}, t0.Add(2*time.Hour))

	kv, err := KVProjectionScoped(ctx, store, KVProjectionOptions{Scope: KVScopeProject, ProjectID: "proj_a"}, []string{"ws_1"})
	if err != nil {
		t.Fatalf("KVProjectionScoped: %v", err)
	}
	if got, want := len(kv), 1; got != want {
		t.Fatalf("proj_a kv len = %d, want %d", got, want)
	}
	if v := kv["max_retries"].Value; v != "10" {
		t.Errorf("proj_a max_retries = %q, want %q (newest wins)", v, "10")
	}
}

func TestKVProjectionScoped_User(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	t0 := time.Now().UTC()

	// Two users in the same workspace, same key.
	_, _ = store.Append(ctx, LedgerEntry{WorkspaceID: "ws_1", Type: TypeKV, Tags: []string{"scope:user", "user:alice", "key:theme"}, Content: "dark"}, t0)
	_, _ = store.Append(ctx, LedgerEntry{WorkspaceID: "ws_1", Type: TypeKV, Tags: []string{"scope:user", "user:bob", "key:theme"}, Content: "light"}, t0)

	aliceKV, err := KVProjectionScoped(ctx, store, KVProjectionOptions{Scope: KVScopeUser, WorkspaceID: "ws_1", UserID: "alice"}, []string{"ws_1"})
	if err != nil {
		t.Fatalf("KVProjectionScoped: %v", err)
	}
	if got, want := len(aliceKV), 1; got != want {
		t.Fatalf("alice kv len = %d, want %d", got, want)
	}
	if v := aliceKV["theme"].Value; v != "dark" {
		t.Errorf("alice theme = %q, want %q", v, "dark")
	}
	if u := aliceKV["theme"].UserID; u != "alice" {
		t.Errorf("alice theme UserID = %q, want %q", u, "alice")
	}

	bobKV, err := KVProjectionScoped(ctx, store, KVProjectionOptions{Scope: KVScopeUser, WorkspaceID: "ws_1", UserID: "bob"}, []string{"ws_1"})
	if err != nil {
		t.Fatalf("KVProjectionScoped: %v", err)
	}
	if v := bobKV["theme"].Value; v != "light" {
		t.Errorf("bob theme = %q, want %q (alice's row leaked)", v, "light")
	}
}

func TestKVProjectionScoped_LegacyRowDefaultsToProject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	t0 := time.Now().UTC()

	// Pre-story_61abf197 row: no scope tag, only key tag.
	_, _ = store.Append(ctx, LedgerEntry{WorkspaceID: "ws_1", ProjectID: "proj_a", Type: TypeKV, Tags: []string{"key:workflow_spec"}, Content: "legacy"}, t0)

	kv, err := KVProjectionScoped(ctx, store, KVProjectionOptions{Scope: KVScopeProject, ProjectID: "proj_a"}, []string{"ws_1"})
	if err != nil {
		t.Fatalf("KVProjectionScoped: %v", err)
	}
	if got, want := len(kv), 1; got != want {
		t.Fatalf("legacy row not visible: len = %d, want %d", got, want)
	}
	if s := kv["workflow_spec"].Scope; s != KVScopeProject {
		t.Errorf("legacy row Scope = %q, want %q (default)", s, KVScopeProject)
	}
}

func TestKVProjectionScoped_RequiresScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	if _, err := KVProjectionScoped(ctx, store, KVProjectionOptions{}, nil); err == nil {
		t.Fatal("KVProjectionScoped accepted empty Scope; want error")
	}
}

func TestKVProjection_RederivedAfterMutation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	t0 := time.Now().UTC()

	_, _ = store.Append(ctx, LedgerEntry{ProjectID: "proj_a", Type: TypeKV, Tags: []string{"key:foo"}, Content: "v1"}, t0)
	first, _ := KVProjection(ctx, store, "proj_a", nil)
	if first["foo"].Value != "v1" {
		t.Fatalf("first projection foo = %q, want v1", first["foo"].Value)
	}

	_, _ = store.Append(ctx, LedgerEntry{ProjectID: "proj_a", Type: TypeKV, Tags: []string{"key:foo"}, Content: "v2"}, t0.Add(time.Hour))
	second, _ := KVProjection(ctx, store, "proj_a", nil)
	if second["foo"].Value != "v2" {
		t.Errorf("second projection foo = %q, want v2 (re-derived)", second["foo"].Value)
	}
}

func TestStoryTimeline_AscendingByCreatedAt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	t0 := time.Now().UTC()
	storyID := "sty_1"
	otherStory := "sty_2"

	// Insert out of order on purpose; derivation must sort ASC.
	_, _ = store.Append(ctx, LedgerEntry{ProjectID: "proj_a", Type: TypeDecision, StoryID: &storyID, Content: "c"}, t0.Add(2*time.Hour))
	_, _ = store.Append(ctx, LedgerEntry{ProjectID: "proj_a", Type: TypeDecision, StoryID: &storyID, Content: "a"}, t0)
	_, _ = store.Append(ctx, LedgerEntry{ProjectID: "proj_a", Type: TypeDecision, StoryID: &storyID, Content: "b"}, t0.Add(time.Hour))
	_, _ = store.Append(ctx, LedgerEntry{ProjectID: "proj_a", Type: TypeDecision, StoryID: &otherStory, Content: "other"}, t0.Add(30*time.Minute))

	timeline, err := StoryTimeline(ctx, store, storyID, nil)
	if err != nil {
		t.Fatalf("StoryTimeline: %v", err)
	}
	if len(timeline) != 3 {
		t.Fatalf("len = %d, want 3 (other story excluded)", len(timeline))
	}
	if timeline[0].Content != "a" || timeline[1].Content != "b" || timeline[2].Content != "c" {
		t.Errorf("timeline order = %v, want a,b,c", []string{timeline[0].Content, timeline[1].Content, timeline[2].Content})
	}
}

func TestCostRollup_SumsLLMUsageOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	t0 := time.Now().UTC()

	// Two llm-usage rows with structured cost; one llm-usage row with
	// invalid JSON; one untagged row that must be excluded.
	_, _ = store.Append(ctx, LedgerEntry{ProjectID: "proj_a", Type: TypeDecision, Tags: []string{"kind:llm-usage"}, Structured: []byte(`{"cost_usd":0.012,"input_tokens":1000,"output_tokens":500}`)}, t0)
	_, _ = store.Append(ctx, LedgerEntry{ProjectID: "proj_a", Type: TypeDecision, Tags: []string{"kind:llm-usage"}, Structured: []byte(`{"cost_usd":0.034,"input_tokens":2500,"output_tokens":750}`)}, t0.Add(time.Second))
	_, _ = store.Append(ctx, LedgerEntry{ProjectID: "proj_a", Type: TypeDecision, Tags: []string{"kind:llm-usage"}, Structured: []byte(`not-json`)}, t0.Add(2*time.Second))
	_, _ = store.Append(ctx, LedgerEntry{ProjectID: "proj_a", Type: TypeDecision, Tags: []string{"kind:other"}, Structured: []byte(`{"cost_usd":99.0}`)}, t0.Add(3*time.Second))

	summary, err := CostRollup(ctx, store, "proj_a", nil)
	if err != nil {
		t.Fatalf("CostRollup: %v", err)
	}
	if summary.RowCount != 3 {
		t.Errorf("RowCount = %d, want 3 (other-tagged row excluded)", summary.RowCount)
	}
	if summary.SkippedRows != 1 {
		t.Errorf("SkippedRows = %d, want 1 (invalid JSON)", summary.SkippedRows)
	}
	wantCost := 0.012 + 0.034
	if summary.CostUSD < wantCost-1e-9 || summary.CostUSD > wantCost+1e-9 {
		t.Errorf("CostUSD = %v, want %v", summary.CostUSD, wantCost)
	}
	if summary.InputTokens != 3500 {
		t.Errorf("InputTokens = %d, want 3500", summary.InputTokens)
	}
	if summary.OutputTokens != 1250 {
		t.Errorf("OutputTokens = %d, want 1250", summary.OutputTokens)
	}
}

func TestDerivations_ReadOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	t0 := time.Now().UTC()

	// Seed a few rows.
	_, _ = store.Append(ctx, LedgerEntry{ProjectID: "proj_a", Type: TypeKV, Tags: []string{"key:foo"}, Content: "v"}, t0)
	storyID := "sty_x"
	_, _ = store.Append(ctx, LedgerEntry{ProjectID: "proj_a", Type: TypeDecision, StoryID: &storyID, Content: "evt"}, t0.Add(time.Second))
	_, _ = store.Append(ctx, LedgerEntry{ProjectID: "proj_a", Type: TypeDecision, Tags: []string{"kind:llm-usage"}, Structured: []byte(`{"cost_usd":1}`)}, t0.Add(2*time.Second))

	before, _ := store.List(ctx, "proj_a", ListOptions{Limit: 500, IncludeDerefd: true}, nil)
	beforeCount := len(before)

	if _, err := KVProjection(ctx, store, "proj_a", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := StoryTimeline(ctx, store, storyID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := CostRollup(ctx, store, "proj_a", nil); err != nil {
		t.Fatal(err)
	}

	after, _ := store.List(ctx, "proj_a", ListOptions{Limit: 500, IncludeDerefd: true}, nil)
	if len(after) != beforeCount {
		t.Errorf("derivations wrote rows: before=%d after=%d", beforeCount, len(after))
	}
}
