package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"testing"
)

// TestSelectWorkflowSkill pins index-derived dispatch (sty_815c09e7): the
// workflow is the kind:workflow entry whose applies_to contains the story
// type; a gate kind is never selected; an unbound type errors.
func TestSelectWorkflowSkill(t *testing.T) {
	index := []skillIndexEntry{
		{Name: "satellites-feature-workflow", Kind: "workflow", AppliesTo: []string{"feature"}, LocalName: "satellites-feature-workflow"},
		{Name: "satellites-fix-workflow", Kind: "workflow", AppliesTo: []string{"fix", "refactor", "bug", "infrastructure"}, LocalName: "satellites-fix-workflow"},
		// A gate that (hypothetically) lists a story type must not win.
		{Name: "satellites-story-done-review", Kind: "gate", AppliesTo: []string{"feature"}, LocalName: "satellites-story-done-review"},
	}
	want := map[string]string{
		"feature":        filepath.Join(".claude", "skills", "satellites-feature-workflow", "SKILL.md"),
		"fix":            filepath.Join(".claude", "skills", "satellites-fix-workflow", "SKILL.md"),
		"infrastructure": filepath.Join(".claude", "skills", "satellites-fix-workflow", "SKILL.md"),
	}
	for storyType, wantPath := range want {
		got, err := selectWorkflowSkill(index, storyType)
		if err != nil {
			t.Errorf("selectWorkflowSkill(%q): %v", storyType, err)
			continue
		}
		if got != wantPath {
			t.Errorf("selectWorkflowSkill(%q) = %q, want %q", storyType, got, wantPath)
		}
	}
	if _, err := selectWorkflowSkill(index, "nonesuch"); err == nil {
		t.Error("expected error for a story type bound to no kind:workflow skill")
	}
}

// TestBuildSkillIndex confirms the index projects frontmatter dispatch fields
// (kind/applies_to) and the materialised local name, with the body excluded.
func TestBuildSkillIndex(t *testing.T) {
	const listResp = `{"items":[{"id":"doc_fw","name":"feature-workflow","scope":"project","workspace_id":"wksp_X","project_id":"proj_Y","latest_version":1}]}`
	body := "---\nname: feature-workflow\nkind: workflow\napplies_to: [feature]\nwhen: \"\"\ndescription: the feature lifecycle\n---\n# body kept out of the index\n"
	dispatch := func(_ context.Context, name string, req json.RawMessage) (json.RawMessage, error) {
		switch name {
		case "document_list":
			return json.RawMessage(listResp), nil
		case "document_get":
			return json.RawMessage(`{"raw_body":` + strconv.Quote(body) + `,"document":{"id":"doc_fw","latest_version":1}}`), nil
		}
		return nil, fmt.Errorf("unexpected verb %q", name)
	}
	index, err := buildSkillIndex(context.Background(), dispatch, "project", "wksp_X", "proj_Y")
	if err != nil {
		t.Fatalf("buildSkillIndex: %v", err)
	}
	if len(index) != 1 {
		t.Fatalf("want 1 entry, got %d: %+v", len(index), index)
	}
	e := index[0]
	if e.Kind != "workflow" || len(e.AppliesTo) != 1 || e.AppliesTo[0] != "feature" {
		t.Errorf("dispatch projection wrong: %+v", e)
	}
	// Source name unprefixed; local name carries the satellites- prefix.
	if e.Name != "feature-workflow" || e.LocalName != "satellites-feature-workflow" {
		t.Errorf("name/local-name wrong: name=%q local=%q", e.Name, e.LocalName)
	}
	if e.Description != "the feature lifecycle" {
		t.Errorf("description = %q", e.Description)
	}
}
