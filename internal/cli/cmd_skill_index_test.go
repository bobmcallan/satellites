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

// TestBuildEffectiveSkillIndex_Cascade pins sty_050b6653: the index a project
// resolves against cascades system → project, with the project overriding a
// same-named system baseline and inheriting the rest. A fresh project (no
// project skills) thus runs the loop off the system baseline.
func TestBuildEffectiveSkillIndex_Cascade(t *testing.T) {
	body := func(s string) json.RawMessage {
		return json.RawMessage(`{"raw_body":` + strconv.Quote(s) + `,"document":{"id":"x","latest_version":1}}`)
	}
	dispatch := func(_ context.Context, name string, req json.RawMessage) (json.RawMessage, error) {
		var r struct{ Scope, Name string }
		_ = json.Unmarshal(req, &r)
		switch name {
		case "document_list":
			switch r.Scope {
			case "system":
				return json.RawMessage(`{"items":[` +
					`{"id":"d1","name":"feature-workflow","scope":"system","latest_version":1},` +
					`{"id":"d2","name":"satellites-story-done-review","scope":"system","latest_version":1}]}`), nil
			case "project":
				return json.RawMessage(`{"items":[` +
					`{"id":"d3","name":"feature-workflow","scope":"project","workspace_id":"wksp_X","project_id":"proj_Y","latest_version":1}]}`), nil
			default: // workspace — none
				return json.RawMessage(`{"items":[]}`), nil
			}
		case "document_get":
			switch {
			case r.Name == "feature-workflow" && r.Scope == "system":
				return body("---\nname: feature-workflow\nkind: workflow\napplies_to: [feature]\ndescription: system baseline\n---\nx"), nil
			case r.Name == "feature-workflow" && r.Scope == "project":
				return body("---\nname: feature-workflow\nkind: workflow\napplies_to: [feature]\ndescription: project override\n---\nx"), nil
			case r.Name == "satellites-story-done-review":
				return body("---\nname: satellites-story-done-review\nkind: gate\ndescription: system gate\n---\nx"), nil
			}
			return nil, fmt.Errorf("unexpected get %q/%q", r.Name, r.Scope)
		}
		return nil, fmt.Errorf("unexpected verb %q", name)
	}

	index, err := buildEffectiveSkillIndex(context.Background(), dispatch, "wksp_X", "proj_Y")
	if err != nil {
		t.Fatalf("buildEffectiveSkillIndex: %v", err)
	}
	got := map[string]string{}
	for _, e := range index {
		got[e.Name] = e.Description
	}
	if got["feature-workflow"] != "project override" {
		t.Errorf("feature-workflow should be the PROJECT override, got %q", got["feature-workflow"])
	}
	if got["satellites-story-done-review"] != "system gate" {
		t.Errorf("done-review gate should be INHERITED from system, got %q", got["satellites-story-done-review"])
	}
	// The inherited workflow is still selectable for the story type.
	if _, err := selectWorkflowSkill(index, "feature"); err != nil {
		t.Errorf("selectWorkflowSkill(feature) over the cascade: %v", err)
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
