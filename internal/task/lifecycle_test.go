package task

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// TestLifecycle_Default_AllowsPlannedToPublished covers the built-in
// default matrix — without RegisterLifecycle, ValidTransition still
// permits the canonical planned → published flow.
func TestLifecycle_Default_AllowsPlannedToPublished(t *testing.T) {
	RegisterLifecycle(nil) // ensure default
	if !ValidTransition(StatusPlanned, StatusPublished) {
		t.Fatal("default matrix must allow planned → published")
	}
	if !ValidTransition(StatusPublished, StatusClaimed) {
		t.Fatal("default matrix must allow published → claimed")
	}
	if ValidTransition(StatusPlanned, StatusClaimed) {
		t.Fatal("default matrix must NOT allow planned → claimed (bypass published)")
	}
}

// TestLifecycle_RegisterFromSeed_OverridesDefault covers the runtime
// resolution path: a seed-defined matrix takes precedence over the
// built-in defaults.
func TestLifecycle_RegisterFromSeed_OverridesDefault(t *testing.T) {
	t.Cleanup(func() { RegisterLifecycle(nil) })
	payload, _ := json.Marshal(map[string]any{
		"statuses": []string{"draft", "live", "done"},
		"transitions": map[string][]string{
			"draft": {"live"},
			"live":  {"done"},
		},
		"default_status_on_create":    "draft",
		"subscriber_visible_statuses": []string{"live"},
	})
	lc, err := LifecycleFromStructured(payload)
	if err != nil {
		t.Fatalf("LifecycleFromStructured: %v", err)
	}
	RegisterLifecycle(lc)

	if !ValidTransition("draft", "live") {
		t.Fatal("seed matrix must allow draft → live")
	}
	if ValidTransition("draft", "done") {
		t.Fatal("seed matrix must NOT allow draft → done (bypass live)")
	}
	visible := SubscriberVisibleStatuses()
	if _, ok := visible["live"]; !ok {
		t.Fatal("seed matrix must mark live as subscriber-visible")
	}
	if _, ok := visible["draft"]; ok {
		t.Fatal("seed matrix must NOT mark draft as subscriber-visible")
	}
}

// TestPublish_FlipsPlannedToPublished covers the Publish store path.
func TestPublish_FlipsPlannedToPublished(t *testing.T) {
	RegisterLifecycle(nil)
	store := NewMemoryStore()
	ctx := context.Background()
	now := time.Now().UTC()

	planned, err := store.Enqueue(ctx, Task{
		WorkspaceID: "w",
		Status:      StatusPlanned,
		Origin:      OriginStoryStage,
		Priority:    PriorityMedium,
	}, now)
	if err != nil {
		t.Fatalf("enqueue planned: %v", err)
	}
	if planned.Status != StatusPlanned {
		t.Fatalf("planned status: got %q want planned", planned.Status)
	}

	got, err := store.Publish(ctx, planned.ID, now, []string{"w"})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if got.Status != StatusPublished {
		t.Fatalf("after publish: got status %q want published", got.Status)
	}
}

// TestSubscriberVisible_PlannedHidden covers the subscriber-visibility
// invariant: planned rows are never claimable, even after the legacy
// rows are migrated.
func TestSubscriberVisible_PlannedHidden(t *testing.T) {
	RegisterLifecycle(nil)
	if IsSubscriberVisible(StatusPlanned) {
		t.Fatal("planned must NOT be subscriber-visible")
	}
	if !IsSubscriberVisible(StatusPublished) {
		t.Fatal("published must be subscriber-visible")
	}
}

// TestMigrateEnqueuedToPublished_FlipsLegacyRows covers the boot
// migration: pre-c1200f75 enqueued rows become published.
func TestMigrateEnqueuedToPublished_FlipsLegacyRows(t *testing.T) {
	RegisterLifecycle(nil)
	store := NewMemoryStore()
	ctx := context.Background()
	now := time.Now().UTC()

	for i := 0; i < 3; i++ {
		_, err := store.Enqueue(ctx, Task{
			WorkspaceID: "w",
			Status:      StatusEnqueued,
			Origin:      OriginStoryStage,
			Priority:    PriorityMedium,
		}, now)
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	n, err := MigrateEnqueuedToPublished(ctx, store, now)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if n != 3 {
		t.Fatalf("migrated %d rows, want 3", n)
	}
	rows, _ := store.List(ctx, ListOptions{Status: StatusEnqueued}, []string{"w"})
	if len(rows) != 0 {
		t.Fatalf("after migrate: %d rows still enqueued, want 0", len(rows))
	}
	rows, _ = store.List(ctx, ListOptions{Status: StatusPublished}, []string{"w"})
	if len(rows) != 3 {
		t.Fatalf("after migrate: %d rows published, want 3", len(rows))
	}

	// Idempotent — second run finds no rows.
	n2, err := MigrateEnqueuedToPublished(ctx, store, now)
	if err != nil {
		t.Fatalf("migrate again: %v", err)
	}
	if n2 != 0 {
		t.Fatalf("second run migrated %d, want 0 (idempotent)", n2)
	}
}
