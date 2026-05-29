package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// writeSource creates rootDir/<relPath> with content, making parents.
func writeSource(t *testing.T, rootDir, relPath, content string) {
	t.Helper()
	p := filepath.Join(rootDir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestPlanUpload_PathDerivedIdentity(t *testing.T) {
	root := t.TempDir()
	// Workspace-scope document: config/<wksp>/documents/<name>.md
	writeSource(t, root, "wksp_one/documents/ws-rule.md",
		"---\ntags: [principles:workspace]\n---\n# WS body\n")
	// Project-scope document with a frontmatter name override.
	writeSource(t, root, "wksp_one/proj_one/documents/feature-rule.md",
		"---\nname: overridden-name\ntags: [principles:project]\n---\n# PJ body\n")
	// System seed subtree must be skipped entirely.
	writeSource(t, root, "documents/seed.md",
		"---\nscope: system\n---\n# system seed\n")
	// Non-md files are ignored.
	writeSource(t, root, "wksp_one/proj_one/documents/notes.txt", "ignore")
	// A skill in the same project must not surface for the documents kind.
	writeSource(t, root, "wksp_one/proj_one/skills/a-skill.md",
		"---\n---\n# skill body\n")

	targets, err := planUpload(root, "documents")
	if err != nil {
		t.Fatalf("planUpload: %v", err)
	}

	got := make([]string, 0, len(targets))
	for _, tg := range targets {
		got = append(got, uploadLabel(tg))
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
	for _, tg := range targets {
		tagsBy[uploadLabel(tg)] = tg.Tags
	}
	if tags := tagsBy["workspace/wksp_one/ws-rule"]; !reflect.DeepEqual(tags, []string{"principles:workspace"}) {
		t.Errorf("workspace tags = %v want [principles:workspace]", tags)
	}
	if tags := tagsBy["project/wksp_one/proj_one/overridden-name"]; !reflect.DeepEqual(tags, []string{"principles:project"}) {
		t.Errorf("project tags = %v want [principles:project]", tags)
	}
}

func TestPlanUpload_MissingRoot(t *testing.T) {
	targets, err := planUpload(filepath.Join(t.TempDir(), "does-not-exist"), "documents")
	if err != nil {
		t.Fatalf("expected nil error for missing root, got %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("expected zero targets, got %d", len(targets))
	}
}

func TestPlanUpload_SkillKindFiltersAndType(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "wksp_one/proj_one/skills/my-skill.md",
		"---\n---\n# body\n")
	// A documents-kind file in the same project must be excluded when
	// uploading the skills kind.
	writeSource(t, root, "wksp_one/proj_one/documents/a-doc.md",
		"---\n---\n# body\n")

	targets, err := planUpload(root, "skills")
	if err != nil {
		t.Fatalf("planUpload: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("expected one target, got %d", len(targets))
	}
	if targets[0].Type != "skill" {
		t.Errorf("Type = %q, want skill", targets[0].Type)
	}
	if targets[0].Name != "my-skill" {
		t.Errorf("Name = %q, want my-skill", targets[0].Name)
	}
}

func TestPlanUpload_FrontmatterTypeOverridesDefault(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "wksp_one/proj_one/skills/doc.md",
		"---\ntype: document\n---\n# body\n")
	targets, err := planUpload(root, "skills")
	if err != nil {
		t.Fatalf("planUpload: %v", err)
	}
	if targets[0].Type != "document" {
		t.Errorf("frontmatter type override ignored: %q", targets[0].Type)
	}
}

func TestPlanUpload_UnknownKindDirIsError(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "wksp_one/proj_one/widgets/x.md", "---\n---\n# body\n")
	_, err := planUpload(root, "documents")
	if err == nil || !strings.Contains(err.Error(), "unknown kind directory") {
		t.Fatalf("expected unknown-kind error, got %v", err)
	}
}

func TestPlanUpload_UnexpectedDepthIsError(t *testing.T) {
	root := t.TempDir()
	// Too shallow: config/<wksp>/<file>.md — no kind directory.
	writeSource(t, root, "wksp_one/loose.md", "---\n---\n# body\n")
	_, err := planUpload(root, "documents")
	if err == nil || !strings.Contains(err.Error(), "unexpected source layout") {
		t.Fatalf("expected layout error, got %v", err)
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
