package cli

import (
	"testing"
)

// sty_d7698c22: the live command surface is enumerated without cobra's
// auto-generated help/completion noise.
func TestLiveCommandNames_ExcludesAutogen(t *testing.T) {
	names := liveCommandNames(NewRootCmd())
	if len(names) == 0 {
		t.Fatal("no live commands enumerated")
	}
	for _, n := range names {
		if n == "help" || n == "completion" {
			t.Errorf("auto-generated command %q should be excluded", n)
		}
	}
	// Real commands are present.
	if !contains(names, "update") || !contains(names, "version") {
		t.Errorf("expected real commands in surface, got %v", names)
	}
}

// AC2: a command absent from the doc is reported as drift.
func TestSurfaceDrift_FlagsUndocumented(t *testing.T) {
	doc := "We support `satellites update` and `satellites version`."
	missing := surfaceDrift([]string{"update", "version", "techdebt"}, doc)
	if len(missing) != 1 || missing[0] != "techdebt" {
		t.Fatalf("expected only techdebt missing, got %v", missing)
	}
}

// AC3: once every command is named in the doc, there is no drift.
func TestSurfaceDrift_CleanWhenAllDocumented(t *testing.T) {
	doc := "Commands: update, version, techdebt, surface."
	missing := surfaceDrift([]string{"update", "version", "techdebt", "surface"}, doc)
	if len(missing) != 0 {
		t.Fatalf("expected no drift, got %v", missing)
	}
}

// A command name embedded in a brace-list (e.g. `{document,skill}`)
// counts as documented — whole-word matching spans non-word delimiters.
func TestSurfaceDrift_MatchesInsideBraceList(t *testing.T) {
	doc := "satellites {document,skill,principle} upload"
	missing := surfaceDrift([]string{"document", "skill", "principle"}, doc)
	if len(missing) != 0 {
		t.Fatalf("brace-list commands should match, got missing %v", missing)
	}
}

// A longer command must not be matched by a shorter substring: "doc"
// should not be considered documented just because "document" appears.
func TestSurfaceDrift_WholeWordOnly(t *testing.T) {
	doc := "satellites document upload"
	missing := surfaceDrift([]string{"doc"}, doc)
	if len(missing) != 1 {
		t.Fatalf("substring should not count as documented, got %v", missing)
	}
}
