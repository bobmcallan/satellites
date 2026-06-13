package document

import (
	"strings"
	"testing"
)

// TestResolveKey pins sty_c6de961e: project-scoped resolution drops workspace_id
// (keyed by project_id, name — invariant under a home-workspace move); every
// other scope keeps its workspace_id.
func TestResolveKey(t *testing.T) {
	got := Key{Scope: ScopeProject, WorkspaceID: "wksp_old", ProjectID: "proj_x", Name: "n"}.resolveKey()
	if got.WorkspaceID != "" {
		t.Errorf("project scope must drop workspace_id, got %q", got.WorkspaceID)
	}
	if got.ProjectID != "proj_x" || got.Name != "n" {
		t.Errorf("project_id/name must be preserved, got %+v", got)
	}
	ws := Key{Scope: ScopeWorkspace, WorkspaceID: "wksp_w", Name: "n"}.resolveKey()
	if ws.WorkspaceID != "wksp_w" {
		t.Errorf("workspace scope must keep workspace_id, got %q", ws.WorkspaceID)
	}
}

func TestNewID_Format(t *testing.T) {
	id := NewID()
	if !strings.HasPrefix(id, "doc_") || len(id) != 12 {
		t.Fatalf("malformed id: %q", id)
	}
}

func TestKey_Validate(t *testing.T) {
	cases := []struct {
		name    string
		key     Key
		wantErr bool
	}{
		{"system ok", Key{Scope: ScopeSystem, Name: "x"}, false},
		{"system with workspace_id rejected", Key{Scope: ScopeSystem, WorkspaceID: "wksp_1", Name: "x"}, true},
		{"system with project_id rejected", Key{Scope: ScopeSystem, ProjectID: "proj_1", Name: "x"}, true},
		{"workspace ok", Key{Scope: ScopeWorkspace, WorkspaceID: "wksp_1", Name: "x"}, false},
		{"workspace without workspace_id rejected", Key{Scope: ScopeWorkspace, Name: "x"}, true},
		{"workspace with project_id rejected", Key{Scope: ScopeWorkspace, WorkspaceID: "wksp_1", ProjectID: "proj_1", Name: "x"}, true},
		{"project ok", Key{Scope: ScopeProject, WorkspaceID: "wksp_1", ProjectID: "proj_1", Name: "x"}, false},
		{"project without workspace_id rejected", Key{Scope: ScopeProject, ProjectID: "proj_1", Name: "x"}, true},
		{"project without project_id rejected", Key{Scope: ScopeProject, WorkspaceID: "wksp_1", Name: "x"}, true},
		{"library ok", Key{Scope: ScopeLibrary, ProjectID: "proj_1", Name: "x"}, false},
		{"library without project_id rejected", Key{Scope: ScopeLibrary, Name: "x"}, true},
		{"library with workspace_id rejected", Key{Scope: ScopeLibrary, WorkspaceID: "wksp_1", ProjectID: "proj_1", Name: "x"}, true},
		{"library with user_id rejected", Key{Scope: ScopeLibrary, ProjectID: "proj_1", UserID: "usr_1", Name: "x"}, true},
		{"empty name rejected", Key{Scope: ScopeSystem}, true},
		{"unknown scope rejected", Key{Scope: "other", Name: "x"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.key.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestTagsSettableForType pins which row types SetDocumentTags will
// write. document and skill carry document-level tags directly; story
// and task route through their own update paths. Before sty_598369dd
// only `document` was accepted, so the skill case here would have
// failed — guarding against a regression to that guard.
func TestTagsSettableForType(t *testing.T) {
	cases := map[string]bool{
		TypeDocument: true,
		TypeSkill:    true,
		TypeStory:    false,
		TypeTask:     false,
		"":           false,
		"other":      false,
	}
	for typ, want := range cases {
		if got := tagsSettableForType(typ); got != want {
			t.Errorf("tagsSettableForType(%q) = %v, want %v", typ, got, want)
		}
	}
}
