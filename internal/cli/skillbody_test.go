package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSkillBodyOf_ServerFallback pins sty_27bcfe75 AC2: skillBodyOf resolves a
// capability skill local→server — it returns the server body (frontmatter
// stripped) on a local miss, prefers a present local copy without consulting the
// fetcher, and errors when no source holds the name.
func TestSkillBodyOf_ServerFallback(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	serverBody := "---\nname: satellites-x\ntype: skill\n---\n# server body\n"
	fetch := func(_ context.Context, name string) ([]byte, bool, error) {
		if name == "satellites-x" {
			return []byte(serverBody), true, nil
		}
		return nil, false, nil
	}

	// Local miss → server body, frontmatter stripped.
	got, err := skillBodyOf(context.Background(), fetch, "satellites-x")
	if err != nil {
		t.Fatalf("server fallback: %v", err)
	}
	if !strings.Contains(got, "server body") || strings.Contains(got, "name: satellites-x") {
		t.Fatalf("want frontmatter-stripped server body, got %q", got)
	}

	// Local present → local body wins; the fetcher must NOT be consulted.
	skillDir := filepath.Join(dir, ".claude", "skills", "satellites-x")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: satellites-x\n---\n# local body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = skillBodyOf(context.Background(), func(context.Context, string) ([]byte, bool, error) {
		t.Fatal("fetcher consulted despite a present local copy")
		return nil, false, nil
	}, "satellites-x")
	if err != nil {
		t.Fatalf("local read: %v", err)
	}
	if !strings.Contains(got, "local body") {
		t.Fatalf("want local body, got %q", got)
	}

	// No local + fetch miss → error (no source).
	if _, err := skillBodyOf(context.Background(), fetch, "satellites-absent"); err == nil {
		t.Fatal("a name no source holds must error")
	}
}
