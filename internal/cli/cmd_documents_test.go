package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestPlanDocumentsUpload(t *testing.T) {
	root := t.TempDir()
	docsRoot := filepath.Join(root, "documents")

	mustWrite := func(rel, content string) {
		t.Helper()
		p := filepath.Join(docsRoot, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	mustWrite("workspace/wksp_one/ws-rule.md", "---\ntags: [principles:workspace]\n---\n# WS body\n")
	mustWrite("project/wksp_one/proj_one/feature-rule.md", "---\ntags: [principles:project]\nname: overridden-name\n---\n# PJ body\n")
	mustWrite("project/wksp_one/proj_one/no-frontmatter.md", "# raw body, no frontmatter\n")
	// Files at the wrong depth should be silently skipped.
	mustWrite("loose.md", "skip me")
	mustWrite("project/orphan.md", "skip me too")
	// Non-md files should be ignored.
	mustWrite("workspace/wksp_one/notes.txt", "ignore")

	targets, err := planDocumentsUpload(docsRoot)
	if err != nil {
		t.Fatalf("planDocumentsUpload: %v", err)
	}

	got := make([]string, 0, len(targets))
	for _, t := range targets {
		got = append(got, uploadLabel(t))
	}
	sort.Strings(got)
	want := []string{
		"project/wksp_one/proj_one/no-frontmatter",
		"project/wksp_one/proj_one/overridden-name",
		"workspace/wksp_one/ws-rule",
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets mismatch:\n got  %v\n want %v", got, want)
	}

	// Spot-check the tags carried through frontmatter:
	tagsBy := map[string][]string{}
	for _, t := range targets {
		tagsBy[uploadLabel(t)] = t.Tags
	}
	if tags := tagsBy["workspace/wksp_one/ws-rule"]; !reflect.DeepEqual(tags, []string{"principles:workspace"}) {
		t.Errorf("workspace tags = %v want [principles:workspace]", tags)
	}
	if tags := tagsBy["project/wksp_one/proj_one/overridden-name"]; !reflect.DeepEqual(tags, []string{"principles:project"}) {
		t.Errorf("project tags = %v want [principles:project]", tags)
	}
	if tags := tagsBy["project/wksp_one/proj_one/no-frontmatter"]; tags != nil {
		t.Errorf("no-frontmatter should yield nil tags, got %v", tags)
	}
}

func TestPlanDocumentsUpload_MissingRoot(t *testing.T) {
	targets, err := planDocumentsUpload(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("expected nil error for missing root, got %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("expected zero targets, got %d", len(targets))
	}
}
