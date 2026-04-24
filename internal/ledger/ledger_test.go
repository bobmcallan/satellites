package ledger

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNewID_Format(t *testing.T) {
	t.Parallel()
	id := NewID()
	if !strings.HasPrefix(id, "ldg_") || len(id) != len("ldg_")+8 {
		t.Errorf("id %q has wrong shape", id)
	}
	if NewID() == id {
		t.Error("NewID must mint unique ids")
	}
}

// TestStoreInterface_AppendOnly pins the Store surface: a change that adds
// Update/Delete/GetByID to the interface would fail this test (via the
// reflect walk) and the compile-time `var _ Store = ...` assertion in
// store.go / surreal.go.
//
// BackfillWorkspaceID is allow-listed — it only stamps workspace_id on
// rows where it was empty and is scoped to the feature-order:2 migration.
func TestStoreInterface_AppendOnly(t *testing.T) {
	t.Parallel()
	want := map[string]bool{"Append": true, "List": true, "BackfillWorkspaceID": true}
	typ := reflect.TypeOf((*Store)(nil)).Elem()
	if typ.NumMethod() != len(want) {
		t.Fatalf("Store declares %d methods; want exactly %d (%v)", typ.NumMethod(), len(want), want)
	}
	for i := 0; i < typ.NumMethod(); i++ {
		m := typ.Method(i).Name
		if !want[m] {
			t.Errorf("unexpected method on Store: %q (append-only interface: Append + List + BackfillWorkspaceID)", m)
		}
	}
}

func TestMemoryStore_AppendStampsIDAndTime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Now().UTC()

	e, err := store.Append(ctx, LedgerEntry{
		ProjectID: "proj_a",
		Type:      TypeDecision,
		Content:   "hello",
		CreatedBy: "u_1",
	}, now)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if !strings.HasPrefix(e.ID, "ldg_") {
		t.Errorf("id %q not stamped", e.ID)
	}
	if !e.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt = %v, want %v", e.CreatedAt, now)
	}
	if e.ProjectID != "proj_a" || e.Type != TypeDecision || e.Content != "hello" || e.CreatedBy != "u_1" {
		t.Errorf("fields round-trip mismatch: %+v", e)
	}
	if e.Durability != DurabilityDurable || e.SourceType != SourceAgent || e.Status != StatusActive {
		t.Errorf("defaults missing: %+v", e)
	}
}

func TestMemoryStore_ListNewestFirst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	_, _ = store.Append(ctx, LedgerEntry{ProjectID: "proj_a", Type: TypePlan, CreatedBy: "u_1"}, t0)
	_, _ = store.Append(ctx, LedgerEntry{ProjectID: "proj_a", Type: TypeArtifact, CreatedBy: "u_1"}, t0.Add(time.Hour))
	_, _ = store.Append(ctx, LedgerEntry{ProjectID: "proj_a", Type: TypeEvidence, CreatedBy: "u_1"}, t0.Add(2*time.Hour))

	got, err := store.List(ctx, "proj_a", ListOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].Type != TypeEvidence || got[1].Type != TypeArtifact || got[2].Type != TypePlan {
		t.Errorf("unexpected order: %v", []string{got[0].Type, got[1].Type, got[2].Type})
	}
}

func TestMemoryStore_ListTypeFilter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Now().UTC()

	_, _ = store.Append(ctx, LedgerEntry{ProjectID: "proj_a", Type: TypeDecision}, now)
	_, _ = store.Append(ctx, LedgerEntry{ProjectID: "proj_a", Type: TypeDecision}, now.Add(time.Second))
	_, _ = store.Append(ctx, LedgerEntry{ProjectID: "proj_a", Type: TypeEvidence}, now.Add(2*time.Second))

	got, _ := store.List(ctx, "proj_a", ListOptions{Type: TypeDecision}, nil)
	if len(got) != 2 {
		t.Errorf("type filter returned %d, want 2", len(got))
	}
	for _, e := range got {
		if e.Type != TypeDecision {
			t.Errorf("leaked %q", e.Type)
		}
	}
}

func TestMemoryStore_ListLimitClamp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Now().UTC()
	for i := 0; i < 600; i++ {
		_, _ = store.Append(ctx, LedgerEntry{ProjectID: "proj_a", Type: TypeDecision}, now.Add(time.Duration(i)*time.Microsecond))
	}

	got, _ := store.List(ctx, "proj_a", ListOptions{}, nil)
	if len(got) != DefaultListLimit {
		t.Errorf("default limit returned %d, want %d", len(got), DefaultListLimit)
	}
	got, _ = store.List(ctx, "proj_a", ListOptions{Limit: 2}, nil)
	if len(got) != 2 {
		t.Errorf("limit 2 returned %d, want 2", len(got))
	}
	got, _ = store.List(ctx, "proj_a", ListOptions{Limit: 9999}, nil)
	if len(got) != MaxListLimit {
		t.Errorf("ceiling clamp returned %d, want %d", len(got), MaxListLimit)
	}
}

func TestMemoryStore_ProjectIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Now().UTC()
	_, _ = store.Append(ctx, LedgerEntry{ProjectID: "proj_a", Type: TypeDecision}, now)
	_, _ = store.Append(ctx, LedgerEntry{ProjectID: "proj_b", Type: TypeDecision}, now)

	a, _ := store.List(ctx, "proj_a", ListOptions{}, nil)
	b, _ := store.List(ctx, "proj_b", ListOptions{}, nil)
	c, _ := store.List(ctx, "proj_missing", ListOptions{}, nil)
	if len(a) != 1 || len(b) != 1 {
		t.Errorf("per-project counts wrong: a=%d b=%d", len(a), len(b))
	}
	if len(c) != 0 {
		t.Errorf("missing project should return empty, got %d", len(c))
	}
}

func TestValidate_TypeEnum(t *testing.T) {
	t.Parallel()
	for _, typ := range []string{TypePlan, TypeActionClaim, TypeArtifact, TypeEvidence, TypeDecision, TypeCloseRequest, TypeVerdict, TypeWorkflowClaim, TypeKV} {
		e := LedgerEntry{Type: typ, Durability: DurabilityDurable, SourceType: SourceAgent, Status: StatusActive}
		if err := e.Validate(); err != nil {
			t.Errorf("Validate(type=%q) rejected: %v", typ, err)
		}
	}
	for _, bad := range []string{"", "story.status_change", "garbage"} {
		e := LedgerEntry{Type: bad, Durability: DurabilityDurable, SourceType: SourceAgent, Status: StatusActive}
		if err := e.Validate(); err == nil {
			t.Errorf("Validate(type=%q) accepted; want rejection", bad)
		}
	}
}

func TestValidate_DurabilityEnum(t *testing.T) {
	t.Parallel()
	for _, dur := range []string{DurabilityPipeline, DurabilityDurable} {
		e := LedgerEntry{Type: TypeDecision, Durability: dur, SourceType: SourceAgent, Status: StatusActive}
		if err := e.Validate(); err != nil {
			t.Errorf("Validate(durability=%q) rejected: %v", dur, err)
		}
	}
	for _, bad := range []string{"", "permanent"} {
		e := LedgerEntry{Type: TypeDecision, Durability: bad, SourceType: SourceAgent, Status: StatusActive}
		if err := e.Validate(); err == nil {
			t.Errorf("Validate(durability=%q) accepted; want rejection", bad)
		}
	}
}

func TestValidate_ExpiresAtRequiredWhenEphemeral(t *testing.T) {
	t.Parallel()
	naked := LedgerEntry{Type: TypeDecision, Durability: DurabilityEphemeral, SourceType: SourceAgent, Status: StatusActive}
	if err := naked.Validate(); err == nil {
		t.Error("ephemeral without expires_at accepted; want rejection")
	}
	expiry := time.Now().Add(time.Hour)
	ok := naked
	ok.ExpiresAt = &expiry
	if err := ok.Validate(); err != nil {
		t.Errorf("ephemeral with expires_at rejected: %v", err)
	}
	leaked := LedgerEntry{Type: TypeDecision, Durability: DurabilityDurable, SourceType: SourceAgent, Status: StatusActive, ExpiresAt: &expiry}
	if err := leaked.Validate(); err == nil {
		t.Error("durable with expires_at accepted; want rejection")
	}
}

func TestValidate_SourceTypeEnum(t *testing.T) {
	t.Parallel()
	for _, src := range []string{SourceManifest, SourceFeedback, SourceAgent, SourceUser, SourceSystem, SourceMigration} {
		e := LedgerEntry{Type: TypeDecision, Durability: DurabilityDurable, SourceType: src, Status: StatusActive}
		if err := e.Validate(); err != nil {
			t.Errorf("Validate(source_type=%q) rejected: %v", src, err)
		}
	}
	for _, bad := range []string{"", "robot"} {
		e := LedgerEntry{Type: TypeDecision, Durability: DurabilityDurable, SourceType: bad, Status: StatusActive}
		if err := e.Validate(); err == nil {
			t.Errorf("Validate(source_type=%q) accepted; want rejection", bad)
		}
	}
}

func TestMemoryStore_Append_RejectsInvalidEnum(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	if _, err := store.Append(ctx, LedgerEntry{ProjectID: "proj_a", Type: "garbage"}, time.Now()); err == nil {
		t.Error("Append accepted bogus type; want rejection")
	}
}
