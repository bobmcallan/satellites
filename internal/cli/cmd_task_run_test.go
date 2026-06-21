package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestResolveWorkSkillLocalFirst pins the contained-skill resolution contract:
// a local .claude/skills/<name>/SKILL.md OVERRIDE wins over the substrate, and
// the substrate fetcher is NOT consulted when the override is present.
func TestResolveWorkSkillLocalFirst(t *testing.T) {
	worktree := t.TempDir()
	name := "secrets-scan"
	dir := filepath.Join(worktree, ".claude", "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("LOCAL OVERRIDE BODY"), 0o644); err != nil {
		t.Fatal(err)
	}

	fetchCalled := false
	fetch := func(ctx context.Context, n string) (string, string, int, error) {
		fetchCalled = true
		return "SUBSTRATE BODY", "project", 1, nil
	}

	got, err := resolveWorkSkill(context.Background(), worktree, name, fetch)
	if err != nil {
		t.Fatalf("resolveWorkSkill: %v", err)
	}
	if got.Origin != "local-override" {
		t.Fatalf("origin = %q, want local-override", got.Origin)
	}
	if got.Body != "LOCAL OVERRIDE BODY" {
		t.Fatalf("body = %q, want the local file", got.Body)
	}
	if fetchCalled {
		t.Fatal("substrate fetcher was called despite a local override being present")
	}
}

// TestResolveWorkSkillSubstrateByValue pins the default path: with no local
// override, the runner binds to the SUBSTRATE copy (by value) and records its
// provenance (scope + version).
func TestResolveWorkSkillSubstrateByValue(t *testing.T) {
	worktree := t.TempDir() // no .claude/skills present

	fetch := func(ctx context.Context, n string) (string, string, int, error) {
		return "SUBSTRATE BODY", "project", 7, nil
	}

	got, err := resolveWorkSkill(context.Background(), worktree, "secrets-scan", fetch)
	if err != nil {
		t.Fatalf("resolveWorkSkill: %v", err)
	}
	if got.Origin != "substrate" {
		t.Fatalf("origin = %q, want substrate", got.Origin)
	}
	if got.Body != "SUBSTRATE BODY" || got.Scope != "project" || got.Version != 7 {
		t.Fatalf("got %+v, want substrate body/project/v7", got)
	}
}

// TestResolveWorkSkillUnresolved: no override AND no substrate copy → error
// (the false-ready case the entry gate also rejects).
func TestResolveWorkSkillUnresolved(t *testing.T) {
	worktree := t.TempDir()
	fetch := func(ctx context.Context, n string) (string, string, int, error) {
		return "", "", 0, os.ErrNotExist
	}
	if _, err := resolveWorkSkill(context.Background(), worktree, "missing", fetch); err == nil {
		t.Fatal("expected an error when the skill resolves nowhere")
	}
}

func TestSkillNameFromTags(t *testing.T) {
	cases := []struct {
		tags []string
		want string
	}{
		{[]string{"epic:x", "skill:secrets-scan", "area:y"}, "secrets-scan"},
		{[]string{"epic:x", "area:y"}, ""},
		{[]string{"skill:  spaced  "}, "spaced"},
		{nil, ""},
	}
	for _, c := range cases {
		if got := skillNameFromTags(c.tags); got != c.want {
			t.Fatalf("skillNameFromTags(%v) = %q, want %q", c.tags, got, c.want)
		}
	}
}
