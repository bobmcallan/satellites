package cli

import (
	"strings"
	"testing"
)

func TestRenderLibraryStamp_Shape(t *testing.T) {
	stamp, err := renderLibraryStamp(libraryProvenance{
		Publisher: "proj_x",
		Repo:      "git@example.com:org/repo.git",
		Commit:    "abc1234",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		libraryStampBegin, libraryStampEnd,
		`"publisher":"proj_x"`, `"repo":"git@example.com:org/repo.git"`, `"commit":"abc1234"`,
	} {
		if !strings.Contains(stamp, want) {
			t.Errorf("stamp missing %q: %s", want, stamp)
		}
	}
	if strings.Contains(stamp, "\n") {
		t.Errorf("stamp must be a single line: %q", stamp)
	}
}

func TestInjectLibraryStamp(t *testing.T) {
	stamp, _ := renderLibraryStamp(libraryProvenance{Publisher: "proj_x"})

	t.Run("after frontmatter close", func(t *testing.T) {
		body := "---\nname: s\ndescription: d\n---\n\n# S\n"
		got := injectLibraryStamp(body, stamp)
		want := "---\nname: s\ndescription: d\n---\n" + stamp + "\n\n# S\n"
		if got != want {
			t.Fatalf("placement wrong:\n got: %q\nwant: %q", got, want)
		}
	})

	t.Run("no frontmatter prepends", func(t *testing.T) {
		got := injectLibraryStamp("# S\n", stamp)
		if !strings.HasPrefix(got, stamp+"\n") {
			t.Fatalf("stamp not prepended: %q", got)
		}
	})

	t.Run("idempotent on re-publish", func(t *testing.T) {
		body := "---\nname: s\n---\n# S\n"
		once := injectLibraryStamp(body, stamp)
		newer, _ := renderLibraryStamp(libraryProvenance{Publisher: "proj_x", Commit: "def5678"})
		twice := injectLibraryStamp(once, newer)
		if n := strings.Count(twice, libraryStampBegin); n != 1 {
			t.Fatalf("stamp count=%d want 1:\n%s", n, twice)
		}
		if !strings.Contains(twice, "def5678") {
			t.Fatalf("re-publish did not replace the stamp:\n%s", twice)
		}
	})
}
