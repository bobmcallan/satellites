package changelog

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestMemoryStore_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Now().UTC()

	c, err := store.Create(ctx, Changelog{
		WorkspaceID: "wksp_a",
		ProjectID:   "proj_a",
		Service:     "satellites",
		VersionFrom: "0.0.165",
		VersionTo:   "0.0.166",
		Content:     "tag chips visible\n\nV3-parity layout fix.",
		CreatedBy:   "u_alice",
	}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.HasPrefix(c.ID, "chg_") {
		t.Errorf("id = %q, want chg_<...>", c.ID)
	}

	got, err := store.GetByID(ctx, c.ID, nil)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.VersionTo != "0.0.166" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestMemoryStore_ListNewestFirstAndProjectScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	mk := func(project, ver string, t time.Time) {
		_, _ = store.Create(ctx, Changelog{
			WorkspaceID: "wksp_a", ProjectID: project, Service: "satellites",
			VersionTo: ver, Content: "v" + ver,
		}, t)
	}
	mk("proj_a", "0.0.158", base.Add(1*time.Minute))
	mk("proj_a", "0.0.159", base.Add(2*time.Minute))
	mk("proj_a", "0.0.160", base.Add(3*time.Minute))
	mk("proj_other", "9.9.9", base.Add(4*time.Minute)) // different project — must not surface

	rows, err := store.List(ctx, ListOptions{ProjectID: "proj_a"}, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3 (proj_other must be excluded)", len(rows))
	}
	want := []string{"0.0.160", "0.0.159", "0.0.158"}
	for i, r := range rows {
		if r.VersionTo != want[i] {
			t.Errorf("rows[%d].VersionTo = %q, want %q (newest-first sort)", i, r.VersionTo, want[i])
		}
	}
}

func TestMemoryStore_UpdatePartialFields(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Now().UTC()
	c, _ := store.Create(ctx, Changelog{
		WorkspaceID: "wksp_a", ProjectID: "proj_a", Service: "satellites",
		VersionTo: "0.0.1", Content: "initial",
	}, now)

	v := "rewritten"
	got, err := store.Update(ctx, c.ID, UpdateFields{Content: &v}, time.Now().UTC(), nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Content != "rewritten" || got.VersionTo != "0.0.1" {
		t.Errorf("partial update mishandled: %+v", got)
	}
}

func TestMemoryStore_DeleteRemovesRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	c, _ := store.Create(ctx, Changelog{WorkspaceID: "wksp_a", ProjectID: "proj_a", Service: "satellites"}, time.Now().UTC())
	if err := store.Delete(ctx, c.ID, nil); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.GetByID(ctx, c.ID, nil); err == nil {
		t.Errorf("GetByID after Delete should fail with ErrNotFound")
	}
}

func TestMemoryStore_MembershipScoping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	c, _ := store.Create(ctx, Changelog{
		WorkspaceID: "wksp_a", ProjectID: "proj_a", Service: "satellites",
	}, time.Now().UTC())

	if _, err := store.GetByID(ctx, c.ID, []string{"wksp_other"}); err == nil {
		t.Errorf("non-member should be denied; got success")
	}
	if _, err := store.GetByID(ctx, c.ID, []string{"wksp_a"}); err != nil {
		t.Errorf("member should be allowed; got %v", err)
	}
}
