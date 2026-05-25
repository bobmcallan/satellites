package reviewer

import (
	"testing"
	"testing/fstest"
)

func TestLoad_ParsesFrontmatterAndBody(t *testing.T) {
	fsys := fstest.MapFS{
		"story_reviewer.md": &fstest.MapFile{Data: []byte(`---
name: story_reviewer
enabled: true
model: claude-sonnet-4-6
max_tokens: 512
---

You are a reviewer.

Output a JSON object.
`)},
	}
	defs, err := Load(fsys)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, ok := defs["story_reviewer"]
	if !ok {
		t.Fatalf("definition missing: %+v", defs)
	}
	if !got.Enabled {
		t.Fatalf("enabled = false, want true")
	}
	if got.Model != "claude-sonnet-4-6" {
		t.Fatalf("model = %q", got.Model)
	}
	if got.MaxTokens != 512 {
		t.Fatalf("max_tokens = %d", got.MaxTokens)
	}
	if got.Body == "" {
		t.Fatalf("body empty")
	}
}

func TestLoad_SkipsNonMarkdown(t *testing.T) {
	fsys := fstest.MapFS{
		"readme.txt": &fstest.MapFile{Data: []byte("ignore me")},
	}
	defs, err := Load(fsys)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(defs) != 0 {
		t.Fatalf("expected empty, got %+v", defs)
	}
}

func TestLoad_RejectsDuplicateNames(t *testing.T) {
	fsys := fstest.MapFS{
		"a.md": &fstest.MapFile{Data: []byte("---\nname: dup\n---\nbody")},
		"b.md": &fstest.MapFile{Data: []byte("---\nname: dup\n---\nbody")},
	}
	if _, err := Load(fsys); err == nil {
		t.Fatalf("expected duplicate-name error")
	}
}

func TestLoad_MissingNameIsAnError(t *testing.T) {
	fsys := fstest.MapFS{
		"unnamed.md": &fstest.MapFile{Data: []byte("---\nenabled: true\n---\nbody")},
	}
	if _, err := Load(fsys); err == nil {
		t.Fatalf("expected missing-name error")
	}
}
