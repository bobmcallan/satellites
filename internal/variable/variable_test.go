package variable

import (
	"strings"
	"testing"
)

func TestNewID_Format(t *testing.T) {
	id := NewID()
	if !strings.HasPrefix(id, "var_") || len(id) != 12 {
		t.Fatalf("malformed id: %q", id)
	}
}

func TestKey_Validate(t *testing.T) {
	cases := []struct {
		name    string
		key     Key
		wantErr bool
	}{
		{"workspace ok", Key{Scope: ScopeWorkspace, WorkspaceID: "wksp_1", Name: "x"}, false},
		{"workspace without workspace_id rejected", Key{Scope: ScopeWorkspace, Name: "x"}, true},
		{"workspace with project_id rejected", Key{Scope: ScopeWorkspace, WorkspaceID: "wksp_1", ProjectID: "proj_1", Name: "x"}, true},
		{"project ok", Key{Scope: ScopeProject, WorkspaceID: "wksp_1", ProjectID: "proj_1", Name: "x"}, false},
		{"project without project_id rejected", Key{Scope: ScopeProject, WorkspaceID: "wksp_1", Name: "x"}, true},
		{"system bare ok (resolver-only)", Key{Scope: ScopeSystem, Name: "x"}, false},
		{"system with workspace_id rejected", Key{Scope: ScopeSystem, WorkspaceID: "wksp_1", Name: "x"}, true},
		{"empty name rejected", Key{Scope: ScopeSystem}, true},
		{"unknown scope rejected", Key{Scope: "other", Name: "x"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.key.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
