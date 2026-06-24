package cli

import (
	"reflect"
	"testing"
)

// TestStoryCreateRequest pins the CLI → document_upsert mapping (sty_5d7e6baf):
// `story create` is a thin wrapper over the SAME story-create verb the MCP uses,
// type is always "story", name/project trim, and pointer-typed metadata is set
// only when the flag was provided (so an unset field is left to server defaults,
// not written as an explicit empty).
func TestStoryCreateRequest(t *testing.T) {
	// Minimal: only required-ish fields → no metadata pointers set.
	min := storyCreateRequest(storyCreateOpts{Name: "  hello  ", Project: " proj_x "})
	if min.Type != "story" {
		t.Errorf("Type = %q, want story", min.Type)
	}
	if min.Name != "hello" || min.ProjectID != "proj_x" {
		t.Errorf("name/project not trimmed: %q / %q", min.Name, min.ProjectID)
	}
	for _, p := range []any{min.Tags, min.Category, min.Priority, min.ParentID, min.Status} {
		if !reflect.ValueOf(p).IsNil() {
			t.Errorf("unset metadata must be nil, got %#v", p)
		}
	}

	// Full: every flag provided → every pointer set to the trimmed value.
	full := storyCreateRequest(storyCreateOpts{
		Name: "story", Project: "proj_y", Body: "# body",
		Tags:     []string{"epic:foo", "area:bar"},
		Category: " feature ", Priority: " high ", Parent: " sty_p ", Status: " backlog ",
	})
	if full.Body != "# body" {
		t.Errorf("body = %q", full.Body)
	}
	if full.Tags == nil || !reflect.DeepEqual(*full.Tags, []string{"epic:foo", "area:bar"}) {
		t.Errorf("tags = %v", full.Tags)
	}
	if full.Category == nil || *full.Category != "feature" {
		t.Errorf("category = %v (want feature, trimmed)", full.Category)
	}
	if full.Priority == nil || *full.Priority != "high" {
		t.Errorf("priority = %v", full.Priority)
	}
	if full.ParentID == nil || *full.ParentID != "sty_p" {
		t.Errorf("parent = %v", full.ParentID)
	}
	if full.Status == nil || *full.Status != "backlog" {
		t.Errorf("status = %v", full.Status)
	}
}
