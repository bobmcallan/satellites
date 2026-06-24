package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// writeSkill creates a minimal valid skill file under .satellites/skills/.
func writeSkill(t *testing.T, name string) {
	t.Helper()
	dir := filepath.Join(".satellites", "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: test skill " + name + "\n---\n\n# " + name + "\n"
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAllSkillNames(t *testing.T) {
	t.Chdir(t.TempDir())
	writeSkill(t, "beta")
	writeSkill(t, "alpha")
	// A non-skill file is ignored.
	if err := os.WriteFile(filepath.Join(".satellites", "skills", "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := allPublishableNames("skills", "")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"alpha", "beta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("allPublishableNames = %v, want %v", got, want)
	}
}

func TestChangedSkillNames(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	mustGit := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	mustGit("init", "-q")
	mustGit("config", "user.email", "t@example.com")
	mustGit("config", "user.name", "t")
	writeSkill(t, "stable")
	writeSkill(t, "todelete")
	mustGit("add", "-A")
	mustGit("commit", "-q", "-m", "base")
	baseBytes, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	base := string(bytes.TrimSpace(baseBytes))

	// Add one, modify one, delete one — then commit (the CI flow: the changed
	// files are committed on the branch before publish runs).
	writeSkill(t, "fresh") // new
	if err := os.Remove(filepath.Join(".satellites", "skills", "todelete.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(".satellites", "skills", "stable.md"),
		[]byte("---\nname: stable\ndescription: changed\n---\n\n# stable changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit("add", "-A")
	mustGit("commit", "-q", "-m", "change")

	got, err := changedPublishableNames(base, "skills", "")
	if err != nil {
		t.Fatalf("changedPublishableNames: %v", err)
	}
	// fresh (added) + stable (modified); todelete excluded (file gone).
	if want := []string{"fresh", "stable"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("changedPublishableNames = %v, want %v", got, want)
	}

	// An unknown ref is an error, not an empty no-op.
	if _, err := changedPublishableNames("no-such-ref-xyz", "skills", ""); err == nil {
		t.Fatal("expected an error for an unknown git ref")
	}
}

// TestPublishBatch_EmptyIsNoOp: an empty name set is a clean no-op (exit 0).
func TestPublishBatch_EmptyIsNoOp(t *testing.T) {
	var buf bytes.Buffer
	if err := publishBatch(nil, &buf, nil, "skills", "", "", false, false); err != nil {
		t.Fatalf("empty batch should be a no-op, got %v", err)
	}
	if got := buf.String(); got == "" {
		t.Fatal("empty batch should report a no-op line")
	}
}

// TestPublishCmd_SelectorValidation: the selector rules are enforced before any
// publish work (no name/flag, or conflicting selectors → error).
func TestPublishCmd_SelectorValidation(t *testing.T) {
	cfg, usr := "", ""
	run := func(args ...string) error {
		cmd := newSkillPublishCmd(&cfg, &usr)
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(args)
		return cmd.Execute()
	}
	if err := run(); err == nil {
		t.Error("no selector should error")
	}
	if err := run("--all", "somename"); err == nil {
		t.Error("--all + name should error (mutually exclusive)")
	}
	if err := run("--all", "--changed-since", "HEAD"); err == nil {
		t.Error("--all + --changed-since should error (mutually exclusive)")
	}
}
