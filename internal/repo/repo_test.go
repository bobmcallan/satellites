package repo

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *MemoryStore {
	t.Helper()
	return NewMemoryStore()
}

func TestNewID_Format(t *testing.T) {
	t.Parallel()
	id := NewID()
	if !strings.HasPrefix(id, "repo_") {
		t.Fatalf("id %q missing repo_ prefix", id)
	}
	if got := len(id); got != len("repo_")+8 {
		t.Fatalf("id %q has length %d, want %d", id, got, len("repo_")+8)
	}
}

func TestIsKnownStatus(t *testing.T) {
	t.Parallel()
	cases := []struct {
		status string
		ok     bool
	}{
		{StatusActive, true},
		{StatusArchived, true},
		{"", false},
		{"deleted", false},
		{"ACTIVE", false},
	}
	for _, c := range cases {
		if got := IsKnownStatus(c.status); got != c.ok {
			t.Fatalf("IsKnownStatus(%q) = %v, want %v", c.status, got, c.ok)
		}
	}
}

func TestCreate_OnePerProject(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	first, err := store.Create(ctx, Repo{
		WorkspaceID: "ws_1",
		ProjectID:   "proj_a",
		GitRemote:   "git@github.com:example/a.git",
	}, now)
	if err != nil {
		t.Fatalf("first Create: unexpected error %v", err)
	}
	if first.Status != StatusActive {
		t.Fatalf("first Create: status = %q, want %q", first.Status, StatusActive)
	}

	if _, err := store.Create(ctx, Repo{
		WorkspaceID: "ws_1",
		ProjectID:   "proj_a",
		GitRemote:   "git@github.com:example/a-fork.git",
	}, now); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("second active Create: err = %v, want ErrAlreadyExists", err)
	}

	if _, err := store.Archive(ctx, first.ID); err != nil {
		t.Fatalf("Archive: unexpected error %v", err)
	}

	if _, err := store.Create(ctx, Repo{
		WorkspaceID: "ws_1",
		ProjectID:   "proj_a",
		GitRemote:   "git@github.com:example/a-fresh.git",
	}, now); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("Create after archive: err = %v, want ErrAlreadyExists (archived predecessor must still block)", err)
	}
}

func TestArchive_StatusTransition(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	r, err := store.Create(ctx, Repo{
		WorkspaceID: "ws_1",
		ProjectID:   "proj_a",
		GitRemote:   "git@github.com:example/a.git",
	}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if r.Status != StatusActive {
		t.Fatalf("created status = %q, want %q", r.Status, StatusActive)
	}

	archived, err := store.Archive(ctx, r.ID)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if archived.Status != StatusArchived {
		t.Fatalf("archived status = %q, want %q", archived.Status, StatusArchived)
	}
	if !archived.UpdatedAt.After(r.UpdatedAt) && !archived.UpdatedAt.Equal(r.UpdatedAt) {
		t.Fatalf("UpdatedAt did not advance after Archive: before=%v after=%v", r.UpdatedAt, archived.UpdatedAt)
	}

	again, err := store.Archive(ctx, r.ID)
	if err != nil {
		t.Fatalf("re-Archive: unexpected error %v (must be idempotent)", err)
	}
	if again.Status != StatusArchived {
		t.Fatalf("re-archive status = %q, want %q", again.Status, StatusArchived)
	}

	got, err := store.GetByID(ctx, r.ID, nil)
	if err != nil {
		t.Fatalf("GetByID after archive: %v", err)
	}
	if got.Status != StatusArchived {
		t.Fatalf("GetByID status = %q, want %q", got.Status, StatusArchived)
	}
}

func TestGetByRemote_DedupAcrossProjects(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	remote := "git@github.com:example/shared.git"

	a, err := store.Create(ctx, Repo{
		WorkspaceID: "ws_1",
		ProjectID:   "proj_a",
		GitRemote:   remote,
	}, now)
	if err != nil {
		t.Fatalf("Create proj_a: %v", err)
	}

	b, err := store.Create(ctx, Repo{
		WorkspaceID: "ws_1",
		ProjectID:   "proj_b",
		GitRemote:   remote,
	}, now)
	if err != nil {
		t.Fatalf("Create proj_b same remote: %v", err)
	}
	if a.ID == b.ID {
		t.Fatalf("two Creates returned the same id: %s", a.ID)
	}

	got, err := store.GetByRemote(ctx, "ws_1", remote)
	if err != nil {
		t.Fatalf("GetByRemote: %v", err)
	}
	if got.GitRemote != remote {
		t.Fatalf("GetByRemote returned remote=%q, want %q", got.GitRemote, remote)
	}
	if got.ID != a.ID && got.ID != b.ID {
		t.Fatalf("GetByRemote returned id %q, expected one of %q/%q", got.ID, a.ID, b.ID)
	}

	if _, err := store.GetByRemote(ctx, "ws_2", remote); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByRemote in unrelated workspace: err = %v, want ErrNotFound", err)
	}

	if _, err := store.GetByRemote(ctx, "ws_1", "git@github.com:example/missing.git"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByRemote with unknown remote: err = %v, want ErrNotFound", err)
	}
}

func TestList_WorkspaceIsolation(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	if _, err := store.Create(ctx, Repo{
		WorkspaceID: "ws_1",
		ProjectID:   "proj_a",
		GitRemote:   "git@github.com:example/a.git",
	}, now); err != nil {
		t.Fatalf("Create ws_1/proj_a: %v", err)
	}
	if _, err := store.Create(ctx, Repo{
		WorkspaceID: "ws_2",
		ProjectID:   "proj_b",
		GitRemote:   "git@github.com:example/b.git",
	}, now); err != nil {
		t.Fatalf("Create ws_2/proj_b: %v", err)
	}

	scoped, err := store.List(ctx, "proj_a", []string{"ws_1"})
	if err != nil {
		t.Fatalf("List ws_1: %v", err)
	}
	if len(scoped) != 1 || scoped[0].ProjectID != "proj_a" {
		t.Fatalf("List returned %v, want one proj_a row", scoped)
	}

	cross, err := store.List(ctx, "proj_a", []string{"ws_2"})
	if err != nil {
		t.Fatalf("List ws_2 looking for proj_a: %v", err)
	}
	if len(cross) != 0 {
		t.Fatalf("cross-workspace List returned %d rows, want 0", len(cross))
	}

	deny, err := store.List(ctx, "proj_a", []string{})
	if err != nil {
		t.Fatalf("List with empty memberships: %v", err)
	}
	if len(deny) != 0 {
		t.Fatalf("empty memberships List returned %d rows, want 0 (deny-all)", len(deny))
	}

	all, err := store.List(ctx, "proj_a", nil)
	if err != nil {
		t.Fatalf("List with nil memberships: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("nil memberships List returned %d rows, want 1", len(all))
	}
}

func TestGetByID_WorkspaceIsolation(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	r, err := store.Create(ctx, Repo{
		WorkspaceID: "ws_1",
		ProjectID:   "proj_a",
		GitRemote:   "git@github.com:example/a.git",
	}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := store.GetByID(ctx, r.ID, []string{"ws_2"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace GetByID: err = %v, want ErrNotFound", err)
	}
	if _, err := store.GetByID(ctx, r.ID, []string{"ws_1"}); err != nil {
		t.Fatalf("in-workspace GetByID: %v", err)
	}
	if _, err := store.GetByID(ctx, "repo_missing", []string{"ws_1"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id GetByID: err = %v, want ErrNotFound", err)
	}
}

func TestUpdateIndexState_BumpsVersion(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	r, err := store.Create(ctx, Repo{
		WorkspaceID: "ws_1",
		ProjectID:   "proj_a",
		GitRemote:   "git@github.com:example/a.git",
	}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if r.IndexVersion != 0 {
		t.Fatalf("initial index_version = %d, want 0", r.IndexVersion)
	}

	indexedAt := now.Add(time.Minute)
	updated, err := store.UpdateIndexState(ctx, r.ID, "deadbeefcafe", indexedAt, 1234, 56)
	if err != nil {
		t.Fatalf("UpdateIndexState: %v", err)
	}
	if updated.IndexVersion != 1 {
		t.Fatalf("index_version after first update = %d, want 1", updated.IndexVersion)
	}
	if updated.HeadSHA != "deadbeefcafe" {
		t.Fatalf("head_sha = %q, want %q", updated.HeadSHA, "deadbeefcafe")
	}
	if updated.SymbolCount != 1234 || updated.FileCount != 56 {
		t.Fatalf("counts = (%d, %d), want (1234, 56)", updated.SymbolCount, updated.FileCount)
	}
	if !updated.LastIndexedAt.Equal(indexedAt) {
		t.Fatalf("last_indexed_at = %v, want %v", updated.LastIndexedAt, indexedAt)
	}

	updated2, err := store.UpdateIndexState(ctx, r.ID, "feedface0001", indexedAt.Add(time.Minute), 1300, 60)
	if err != nil {
		t.Fatalf("second UpdateIndexState: %v", err)
	}
	if updated2.IndexVersion != 2 {
		t.Fatalf("index_version after second update = %d, want 2", updated2.IndexVersion)
	}

	if _, err := store.UpdateIndexState(ctx, "repo_missing", "x", indexedAt, 0, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateIndexState on unknown id: err = %v, want ErrNotFound", err)
	}
}

func TestStatusEnum_Validates(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	if _, err := store.Create(ctx, Repo{
		WorkspaceID: "ws_1",
		ProjectID:   "proj_a",
		GitRemote:   "git@github.com:example/a.git",
		Status:      "garbage",
	}, now); !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("Create with bad status: err = %v, want ErrInvalidStatus", err)
	}
}
