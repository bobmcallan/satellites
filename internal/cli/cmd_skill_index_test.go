package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
)

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
