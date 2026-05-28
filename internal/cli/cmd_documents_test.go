package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestPlanDocumentsUpload(t *testing.T) {
	root := t.TempDir()
	docsRoot := filepath.Join(root, "documents")
	if err := os.MkdirAll(docsRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	mustWrite := func(name, content string) {
		t.Helper()
		p := filepath.Join(docsRoot, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	mustWrite("ws-rule.md",
		"---\nscope: workspace\nworkspace_id: wksp_one\ntags: [principles:workspace]\n---\n# WS body\n")
	mustWrite("feature-rule.md",
		"---\nscope: project\nworkspace_id: wksp_one\nproject_id: proj_one\nname: overridden-name\ntags: [principles:project]\n---\n# PJ body\n")
	// Non-md files should be ignored.
	mustWrite("notes.txt", "ignore")

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
		"project/wksp_one/proj_one/overridden-name",
		"workspace/wksp_one/ws-rule",
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets mismatch:\n got  %v\n want %v", got, want)
	}

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

func TestPlanDocumentsUpload_MissingScope(t *testing.T) {
	docsRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(docsRoot, "no-scope.md"),
		[]byte("---\ntags: [foo]\n---\n# body\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := planDocumentsUpload(docsRoot)
	if err == nil || !strings.Contains(err.Error(), "scope") {
		t.Fatalf("expected scope-missing error, got %v", err)
	}
}

func TestPlanDocumentsUpload_ScopeMissingIDs(t *testing.T) {
	docsRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(docsRoot, "no-ws.md"),
		[]byte("---\nscope: workspace\n---\n# body\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := planDocumentsUpload(docsRoot)
	if err == nil || !strings.Contains(err.Error(), "workspace_id") {
		t.Fatalf("expected workspace_id-missing error, got %v", err)
	}
}

func TestPlanUpload_SkillDefaultsToSkillType(t *testing.T) {
	stagingRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(stagingRoot, "my-skill.md"),
		[]byte("---\nscope: workspace\nworkspace_id: wksp_one\n---\n# body\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	targets, err := planUpload(stagingRoot, "skill")
	if err != nil {
		t.Fatalf("planUpload: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("expected one target, got %d", len(targets))
	}
	if targets[0].Type != "skill" {
		t.Errorf("Type = %q, want skill", targets[0].Type)
	}
}

func TestPlanUpload_FrontmatterTypeOverridesDefault(t *testing.T) {
	stagingRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(stagingRoot, "doc.md"),
		[]byte("---\nscope: workspace\nworkspace_id: wksp_one\ntype: document\n---\n# body\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	targets, err := planUpload(stagingRoot, "skill")
	if err != nil {
		t.Fatalf("planUpload: %v", err)
	}
	if targets[0].Type != "document" {
		t.Errorf("frontmatter type override ignored: %q", targets[0].Type)
	}
}

func TestMarshalUpsertRequest_TypePassthrough(t *testing.T) {
	target := documentTarget{
		Scope:       "workspace",
		WorkspaceID: "wksp_one",
		Name:        "my-skill",
		Type:        "skill",
		Body:        "# body",
		Tags:        []string{"kind:test"},
	}
	raw, err := marshalUpsertRequest(target)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"type":"skill"`) {
		t.Errorf("payload missing type:\"skill\": %s", raw)
	}
	if !strings.Contains(string(raw), `"workspace_id":"wksp_one"`) {
		t.Errorf("payload missing workspace_id: %s", raw)
	}
}
