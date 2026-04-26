package story

import (
	"context"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/ledger"
)

const cfgTestProject = "proj_cfg_story"
const cfgTestWorkspace = "ws_cfg_story"

func newCfgTestStore(t *testing.T) *MemoryStore {
	t.Helper()
	led := ledger.NewMemoryStore()
	return NewMemoryStore(led)
}

func createTestStory(t *testing.T, store *MemoryStore, title string, now time.Time) Story {
	t.Helper()
	st, err := store.Create(context.Background(), Story{
		WorkspaceID: cfgTestWorkspace,
		ProjectID:   cfgTestProject,
		Title:       title,
		Priority:    "medium",
		Category:    "feature",
	}, now)
	if err != nil {
		t.Fatalf("create story %q: %v", title, err)
	}
	return st
}

func TestStoryUpdate_ConfigurationID_SetClear(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newCfgTestStore(t)
	now := time.Now()
	st := createTestStory(t, store, "set-clear", now)

	if st.ConfigurationID != nil {
		t.Errorf("new story should have nil ConfigurationID, got %v", st.ConfigurationID)
	}

	cfgID := "doc_cfg_alpha"
	updated, err := store.Update(ctx, st.ID, UpdateFields{ConfigurationID: &cfgID}, "test", now.Add(time.Minute), nil)
	if err != nil {
		t.Fatalf("Update set: %v", err)
	}
	if updated.ConfigurationID == nil || *updated.ConfigurationID != cfgID {
		t.Errorf("after set: ConfigurationID = %v, want %q", updated.ConfigurationID, cfgID)
	}

	empty := ""
	cleared, err := store.Update(ctx, st.ID, UpdateFields{ConfigurationID: &empty}, "test", now.Add(2*time.Minute), nil)
	if err != nil {
		t.Fatalf("Update clear: %v", err)
	}
	if cleared.ConfigurationID != nil {
		t.Errorf("after clear: ConfigurationID = %v, want nil", cleared.ConfigurationID)
	}
}

func TestStoryUpdate_ConfigurationID_PreservedWhenNil(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newCfgTestStore(t)
	now := time.Now()
	st := createTestStory(t, store, "preserve", now)

	cfgID := "doc_cfg_beta"
	if _, err := store.Update(ctx, st.ID, UpdateFields{ConfigurationID: &cfgID}, "test", now.Add(time.Minute), nil); err != nil {
		t.Fatalf("Update set: %v", err)
	}

	// UpdateFields with all-nil — should leave ConfigurationID untouched.
	preserved, err := store.Update(ctx, st.ID, UpdateFields{}, "test", now.Add(2*time.Minute), nil)
	if err != nil {
		t.Fatalf("Update no-op: %v", err)
	}
	if preserved.ConfigurationID == nil || *preserved.ConfigurationID != cfgID {
		t.Errorf("after nil-fields Update: ConfigurationID = %v, want %q", preserved.ConfigurationID, cfgID)
	}
}

func TestStoryUpdate_NotFound(t *testing.T) {
	t.Parallel()
	store := newCfgTestStore(t)
	cfgID := "doc_cfg_x"
	_, err := store.Update(context.Background(), "sty_missing", UpdateFields{ConfigurationID: &cfgID}, "test", time.Now(), nil)
	if err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestStoryListByConfigurationID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newCfgTestStore(t)
	now := time.Now()
	a := createTestStory(t, store, "a", now)
	b := createTestStory(t, store, "b", now.Add(time.Minute))
	c := createTestStory(t, store, "c", now.Add(2*time.Minute))

	cfgAlpha := "doc_cfg_alpha"
	cfgBeta := "doc_cfg_beta"
	if _, err := store.Update(ctx, a.ID, UpdateFields{ConfigurationID: &cfgAlpha}, "test", now.Add(3*time.Minute), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Update(ctx, b.ID, UpdateFields{ConfigurationID: &cfgBeta}, "test", now.Add(3*time.Minute), nil); err != nil {
		t.Fatal(err)
	}
	// c stays nil

	matched, err := store.ListByConfigurationID(ctx, cfgAlpha, nil)
	if err != nil {
		t.Fatalf("ListByConfigurationID: %v", err)
	}
	if len(matched) != 1 || matched[0].ID != a.ID {
		t.Errorf("alpha matches: got %v, want [%s]", storyIDs(matched), a.ID)
	}

	betaMatch, err := store.ListByConfigurationID(ctx, cfgBeta, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(betaMatch) != 1 || betaMatch[0].ID != b.ID {
		t.Errorf("beta matches: got %v, want [%s]", storyIDs(betaMatch), b.ID)
	}

	gamma, err := store.ListByConfigurationID(ctx, "doc_cfg_gamma", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(gamma) != 0 {
		t.Errorf("gamma matches: got %v, want []", storyIDs(gamma))
	}

	_ = c // referenced for clarity — c has no configuration_id
}

func TestStoryListByConfigurationID_EmptyIDReturnsNil(t *testing.T) {
	t.Parallel()
	store := newCfgTestStore(t)
	got, err := store.ListByConfigurationID(context.Background(), "", nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil result for empty id, got %v", got)
	}
}

func TestStoryListByConfigurationID_RespectsMemberships(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newCfgTestStore(t)
	now := time.Now()
	st := createTestStory(t, store, "ws-iso", now)
	cfgID := "doc_cfg_x"
	if _, err := store.Update(ctx, st.ID, UpdateFields{ConfigurationID: &cfgID}, "test", now.Add(time.Minute), nil); err != nil {
		t.Fatal(err)
	}

	// Caller is a member of cfgTestWorkspace — sees the row.
	in, err := store.ListByConfigurationID(ctx, cfgID, []string{cfgTestWorkspace})
	if err != nil {
		t.Fatal(err)
	}
	if len(in) != 1 {
		t.Errorf("with matching membership: got %d, want 1", len(in))
	}

	// Caller is a member of a different workspace — sees nothing.
	out, err := store.ListByConfigurationID(ctx, cfgID, []string{"ws_other"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("with mismatched membership: got %d, want 0", len(out))
	}
}

func TestStoryCreate_PersistsConfigurationID(t *testing.T) {
	t.Parallel()
	store := newCfgTestStore(t)
	cfgID := "doc_cfg_at_create"
	st, err := store.Create(context.Background(), Story{
		WorkspaceID:     cfgTestWorkspace,
		ProjectID:       cfgTestProject,
		Title:           "create-with-cfg",
		Priority:        "medium",
		Category:        "feature",
		ConfigurationID: &cfgID,
	}, time.Now())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if st.ConfigurationID == nil || *st.ConfigurationID != cfgID {
		t.Errorf("ConfigurationID after create: got %v, want %q", st.ConfigurationID, cfgID)
	}
}

func storyIDs(ss []Story) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.ID)
	}
	return out
}
