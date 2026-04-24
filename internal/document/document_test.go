package document

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	satarbor "github.com/bobmcallan/satellites/internal/arbor"
)

const testProjectID = "proj_test"

func TestHashBody_Stable(t *testing.T) {
	t.Parallel()
	a := HashBody([]byte("hello"))
	b := HashBody([]byte("hello"))
	if a != b {
		t.Errorf("hash not stable: %q vs %q", a, b)
	}
	c := HashBody([]byte("world"))
	if a == c {
		t.Errorf("distinct bodies must hash differently")
	}
}

func upsertArtifact(t *testing.T, store Store, projectID, name string, body string, now time.Time) UpsertResult {
	t.Helper()
	res, err := store.Upsert(context.Background(), UpsertInput{
		ProjectID: StringPtr(projectID),
		Type:      TypeArtifact,
		Name:      name,
		Body:      []byte(body),
		Scope:     ScopeProject,
		Actor:     "test",
	}, now)
	if err != nil {
		t.Fatalf("Upsert(%q): %v", name, err)
	}
	return res
}

func TestMemoryStore_UpsertIdempotent(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	now := time.Now()

	first := upsertArtifact(t, store, testProjectID, "x.md", "body", now)
	if !first.Created || !first.Changed {
		t.Errorf("first upsert must be Created+Changed: %+v", first)
	}
	if first.Document.Version != 1 {
		t.Errorf("version = %d, want 1", first.Document.Version)
	}
	if first.Document.ProjectID == nil || *first.Document.ProjectID != testProjectID {
		t.Errorf("project_id = %v, want %q", first.Document.ProjectID, testProjectID)
	}

	second := upsertArtifact(t, store, testProjectID, "x.md", "body", now.Add(time.Hour))
	if second.Created || second.Changed {
		t.Errorf("unchanged upsert must be !Created+!Changed: %+v", second)
	}
	if second.Document.Version != 1 {
		t.Errorf("version = %d, want 1 (unchanged)", second.Document.Version)
	}
	if second.Document.ID != first.Document.ID {
		t.Errorf("unchanged upsert minted a new id: %q → %q", first.Document.ID, second.Document.ID)
	}

	third := upsertArtifact(t, store, testProjectID, "x.md", "body2", now.Add(2*time.Hour))
	if third.Created || !third.Changed {
		t.Errorf("changed upsert must be !Created+Changed: %+v", third)
	}
	if third.Document.Version != 2 {
		t.Errorf("version = %d, want 2", third.Document.Version)
	}
	if third.Document.ID != first.Document.ID {
		t.Errorf("changed upsert minted a new id: %q → %q", first.Document.ID, third.Document.ID)
	}
}

func TestMemoryStore_ProjectIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Now()

	upsertArtifact(t, store, "proj_a", "x.md", "A", now)
	upsertArtifact(t, store, "proj_b", "x.md", "B", now)

	a, err := store.GetByName(ctx, "proj_a", "x.md", nil)
	if err != nil {
		t.Fatalf("GetByName proj_a: %v", err)
	}
	if a.Body != "A" {
		t.Errorf("proj_a body = %q, want A", a.Body)
	}

	b, err := store.GetByName(ctx, "proj_b", "x.md", nil)
	if err != nil {
		t.Fatalf("GetByName proj_b: %v", err)
	}
	if b.Body != "B" {
		t.Errorf("proj_b body = %q, want B", b.Body)
	}

	if a.ID == b.ID {
		t.Errorf("distinct projects should mint distinct document ids")
	}

	if nA, _ := store.Count(ctx, "proj_a", nil); nA != 1 {
		t.Errorf("Count(proj_a) = %d, want 1", nA)
	}
	if nB, _ := store.Count(ctx, "proj_b", nil); nB != 1 {
		t.Errorf("Count(proj_b) = %d, want 1", nB)
	}
	if nMissing, _ := store.Count(ctx, "proj_unknown", nil); nMissing != 0 {
		t.Errorf("Count(proj_unknown) = %d, want 0", nMissing)
	}
}

func TestIngestFile_PathTraversalBlocked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	store := NewMemoryStore()
	logger := satarbor.New("info")

	for _, bad := range []string{
		"../etc/passwd",
		"../../secret",
		"/etc/passwd",
		"./../outside.md",
	} {
		if _, err := IngestFile(ctx, store, logger, "", testProjectID, dir, bad, time.Now()); err == nil {
			t.Errorf("expected traversal error for %q", bad)
		}
	}
}

func TestIngestFile_HappyPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "architecture.md"), []byte("# arch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := NewMemoryStore()
	logger := satarbor.New("info")

	res, err := IngestFile(ctx, store, logger, "", testProjectID, dir, "architecture.md", time.Now())
	if err != nil {
		t.Fatalf("IngestFile: %v", err)
	}
	if !res.Created {
		t.Errorf("first ingest must be Created")
	}
	if res.Document.ProjectID == nil || *res.Document.ProjectID != testProjectID {
		t.Errorf("ingested doc project_id = %v, want %q", res.Document.ProjectID, testProjectID)
	}
	if res.Document.Type != TypeArtifact {
		t.Errorf("ingested type = %q, want %q", res.Document.Type, TypeArtifact)
	}
	if res.Document.Scope != ScopeProject {
		t.Errorf("ingested scope = %q, want %q", res.Document.Scope, ScopeProject)
	}
	got, err := store.GetByName(ctx, testProjectID, "architecture.md", nil)
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got.Body != "# arch\n" {
		t.Errorf("body = %q, want \"# arch\\n\"", got.Body)
	}
}

func TestSeed_SkipsWhenProjectPopulated(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "architecture.md"), []byte("# arch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := NewMemoryStore()
	logger := satarbor.New("info")

	upsertArtifact(t, store, testProjectID, "already.md", "x", time.Now())

	n, err := Seed(ctx, store, logger, "", testProjectID, dir, []string{"architecture.md"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("seed ingested %d; expected 0 when project pre-populated", n)
	}
}

func TestSeed_IngestsWhenProjectEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "architecture.md"), []byte("# arch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ui-design.md"), []byte("# design\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := NewMemoryStore()
	logger := satarbor.New("info")
	n, err := Seed(ctx, store, logger, "", testProjectID, dir, []string{"architecture.md", "ui-design.md"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("seed ingested %d; expected 2", n)
	}
}

func TestValidate_TypeEnum(t *testing.T) {
	t.Parallel()
	cases := []struct {
		typ     string
		wantErr bool
	}{
		{TypeArtifact, false},
		{TypeContract, false},
		{TypePrinciple, false},
		{TypeReviewer, false},
		{TypeSkill, false},
		{"", true},
		{"architecture", true},
		{"random", true},
	}
	for _, tc := range cases {
		d := Document{
			Type:  tc.typ,
			Scope: ScopeProject,
			Name:  "x",
		}
		// Reviewer/skill require ContractBinding; supply one for the
		// happy-path branches.
		if tc.typ == TypeSkill || tc.typ == TypeReviewer {
			d.ContractBinding = StringPtr("doc_contract")
		}
		d.ProjectID = StringPtr("proj_x")
		err := d.Validate()
		if tc.wantErr && err == nil {
			t.Errorf("Validate(type=%q) accepted; want rejection", tc.typ)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("Validate(type=%q) rejected: %v", tc.typ, err)
		}
	}
}

func TestValidate_ScopeEnum(t *testing.T) {
	t.Parallel()
	cases := []struct {
		scope   string
		wantErr bool
	}{
		{ScopeProject, false},
		{ScopeSystem, false},
		{"", true},
		{"global", true},
	}
	for _, tc := range cases {
		d := Document{
			Type:  TypeArtifact,
			Scope: tc.scope,
			Name:  "x",
		}
		if tc.scope == ScopeProject {
			d.ProjectID = StringPtr("proj_x")
		}
		err := d.Validate()
		if tc.wantErr && err == nil {
			t.Errorf("Validate(scope=%q) accepted; want rejection", tc.scope)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("Validate(scope=%q) rejected: %v", tc.scope, err)
		}
	}
}

func TestValidate_ProjectIDNullableOnSystem(t *testing.T) {
	t.Parallel()
	// scope=project requires non-nil ProjectID.
	missing := Document{Type: TypeArtifact, Scope: ScopeProject, Name: "x"}
	if err := missing.Validate(); err == nil {
		t.Errorf("scope=project with nil ProjectID accepted; want rejection")
	}
	// scope=system requires nil ProjectID.
	leaked := Document{Type: TypePrinciple, Scope: ScopeSystem, Name: "x", ProjectID: StringPtr("proj_x")}
	if err := leaked.Validate(); err == nil {
		t.Errorf("scope=system with non-nil ProjectID accepted; want rejection")
	}
	// Both happy paths.
	scoped := Document{Type: TypeArtifact, Scope: ScopeProject, Name: "x", ProjectID: StringPtr("proj_x")}
	if err := scoped.Validate(); err != nil {
		t.Errorf("scope=project happy: %v", err)
	}
	system := Document{Type: TypePrinciple, Scope: ScopeSystem, Name: "x"}
	if err := system.Validate(); err != nil {
		t.Errorf("scope=system happy: %v", err)
	}
}

func TestValidate_ContractBindingShape(t *testing.T) {
	t.Parallel()
	// Skill without ContractBinding rejected.
	skillNaked := Document{Type: TypeSkill, Scope: ScopeProject, Name: "s", ProjectID: StringPtr("proj_x")}
	if err := skillNaked.Validate(); err == nil {
		t.Errorf("skill without contract_binding accepted; want rejection")
	}
	// Reviewer without ContractBinding rejected.
	reviewerNaked := Document{Type: TypeReviewer, Scope: ScopeProject, Name: "r", ProjectID: StringPtr("proj_x")}
	if err := reviewerNaked.Validate(); err == nil {
		t.Errorf("reviewer without contract_binding accepted; want rejection")
	}
	// Artifact with ContractBinding rejected.
	artifactBound := Document{Type: TypeArtifact, Scope: ScopeProject, Name: "a", ProjectID: StringPtr("proj_x"), ContractBinding: StringPtr("doc_contract")}
	if err := artifactBound.Validate(); err == nil {
		t.Errorf("artifact with contract_binding accepted; want rejection")
	}
	// Skill happy.
	skill := Document{Type: TypeSkill, Scope: ScopeProject, Name: "s", ProjectID: StringPtr("proj_x"), ContractBinding: StringPtr("doc_contract")}
	if err := skill.Validate(); err != nil {
		t.Errorf("skill happy: %v", err)
	}
}

func TestMemoryStore_DanglingContractBindingRejected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Now()

	// Create a non-contract row at the bound id; the binding should
	// still reject because the target is the wrong type.
	wrongTypeID := "doc_principle"
	if _, err := store.Create(ctx, Document{
		ID:    wrongTypeID,
		Type:  TypePrinciple,
		Scope: ScopeSystem,
		Name:  "p",
		Tags:  []string{"x"},
	}, now); err != nil {
		t.Fatalf("seed principle: %v", err)
	}

	skill := Document{
		Type:            TypeSkill,
		Scope:           ScopeProject,
		Name:            "s",
		ProjectID:       StringPtr("proj_x"),
		ContractBinding: StringPtr("doc_missing"),
	}
	if _, err := store.Create(ctx, skill, now); !errors.Is(err, ErrDanglingBinding) {
		t.Errorf("missing FK: err = %v, want ErrDanglingBinding", err)
	}

	skill.ContractBinding = StringPtr(wrongTypeID)
	if _, err := store.Create(ctx, skill, now); !errors.Is(err, ErrDanglingBinding) {
		t.Errorf("wrong-type FK: err = %v, want ErrDanglingBinding", err)
	}

	// Bind to an active type=contract row → accepted.
	contractID := "doc_contract"
	if _, err := store.Create(ctx, Document{
		ID:    contractID,
		Type:  TypeContract,
		Scope: ScopeSystem,
		Name:  "c",
	}, now); err != nil {
		t.Fatalf("seed contract: %v", err)
	}
	skill.ContractBinding = StringPtr(contractID)
	if _, err := store.Create(ctx, skill, now); err != nil {
		t.Errorf("happy path FK: %v", err)
	}
}

func TestMemoryStore_FilterByType(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Now()

	upsertArtifact(t, store, "proj_x", "a.md", "body", now)
	if _, err := store.Create(ctx, Document{
		Type:  TypePrinciple,
		Scope: ScopeSystem,
		Name:  "p1",
		Tags:  []string{"v4"},
	}, now); err != nil {
		t.Fatalf("Create principle: %v", err)
	}

	got, err := store.List(ctx, ListOptions{Type: TypePrinciple}, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Name != "p1" {
		t.Errorf("List(type=principle) = %+v, want one row p1", got)
	}
}

func TestMemoryStore_FilterByScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Now()

	upsertArtifact(t, store, "proj_x", "a.md", "body", now)
	if _, err := store.Create(ctx, Document{
		Type:  TypePrinciple,
		Scope: ScopeSystem,
		Name:  "p1",
	}, now); err != nil {
		t.Fatalf("Create principle: %v", err)
	}

	got, err := store.List(ctx, ListOptions{Scope: ScopeSystem}, nil)
	if err != nil {
		t.Fatalf("List(scope=system): %v", err)
	}
	if len(got) != 1 || got[0].Scope != ScopeSystem {
		t.Errorf("List(scope=system) = %+v, want one system row", got)
	}
}

func TestMemoryStore_FilterByTags(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Now()

	if _, err := store.Create(ctx, Document{
		Type: TypePrinciple, Scope: ScopeSystem, Name: "p1", Tags: []string{"v4", "process"},
	}, now); err != nil {
		t.Fatalf("Create p1: %v", err)
	}
	if _, err := store.Create(ctx, Document{
		Type: TypePrinciple, Scope: ScopeSystem, Name: "p2", Tags: []string{"infra"},
	}, now); err != nil {
		t.Fatalf("Create p2: %v", err)
	}

	got, err := store.List(ctx, ListOptions{Tags: []string{"v4"}}, nil)
	if err != nil {
		t.Fatalf("List(tags=v4): %v", err)
	}
	if len(got) != 1 || got[0].Name != "p1" {
		t.Errorf("List(tags=v4) = %+v, want p1", got)
	}

	got, err = store.List(ctx, ListOptions{Tags: []string{"v4", "infra"}}, nil)
	if err != nil {
		t.Fatalf("List(tags=v4|infra): %v", err)
	}
	if len(got) != 2 {
		t.Errorf("List(tags=v4|infra) = %d rows, want 2", len(got))
	}
}

func TestMemoryStore_DeleteArchive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Now()

	res := upsertArtifact(t, store, "proj_x", "a.md", "body", now)
	if err := store.Delete(ctx, res.Document.ID, DeleteArchive, nil); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, err := store.GetByID(ctx, res.Document.ID, nil)
	if err != nil {
		t.Fatalf("GetByID after archive: %v", err)
	}
	if got.Status != StatusArchived {
		t.Errorf("after archive status = %q, want %q", got.Status, StatusArchived)
	}
	// Count excludes archived.
	if n, _ := store.Count(ctx, "proj_x", nil); n != 0 {
		t.Errorf("Count after archive = %d, want 0", n)
	}
}

func TestMemoryStore_UpdatePartial(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Now()

	res := upsertArtifact(t, store, "proj_x", "a.md", "body", now)
	newBody := "body-v2"
	tags := []string{"reviewed"}
	updated, err := store.Update(ctx, res.Document.ID, UpdateFields{
		Body: &newBody,
		Tags: &tags,
	}, "alice", now.Add(time.Hour), nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Body != newBody {
		t.Errorf("body = %q, want %q", updated.Body, newBody)
	}
	if updated.Version != 2 {
		t.Errorf("version = %d, want 2", updated.Version)
	}
	if updated.UpdatedBy != "alice" {
		t.Errorf("updated_by = %q, want alice", updated.UpdatedBy)
	}
	if len(updated.Tags) != 1 || updated.Tags[0] != "reviewed" {
		t.Errorf("tags = %v, want [reviewed]", updated.Tags)
	}
}
