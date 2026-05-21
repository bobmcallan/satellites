package verb

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/workspace"
)

func TestWorkspaceMemberVerbs_Registered(t *testing.T) {
	for _, name := range []string{
		"workspace_member_add", "workspace_member_list",
		"workspace_member_update_role", "workspace_member_remove",
	} {
		if Get(name) == nil {
			t.Errorf("verb %q not registered", name)
		}
	}
}

func TestWorkspaceMemberAdd_StoreNotConfigured(t *testing.T) {
	prev := workspaceStore
	workspaceStore = nil
	defer func() { workspaceStore = prev }()
	_, err := Get("workspace_member_add").Invoke(context.Background(),
		json.RawMessage(`{"workspace_id":"w","user_id":"u","role":"admin"}`))
	if err == nil || !strings.Contains(err.Error(), "store not configured") {
		t.Fatalf("expected store-not-configured error, got %v", err)
	}
}

func TestWorkspaceMemberAdd_RequiresIDsAndRole(t *testing.T) {
	prev := workspaceStore
	defer func() { workspaceStore = prev }()
	workspaceStore = &workspace.Store{}

	cases := []struct {
		body string
		want string
	}{
		{`{}`, "workspace_id and user_id required"},
		{`{"workspace_id":"w"}`, "workspace_id and user_id required"},
		{`{"workspace_id":"w","user_id":"u"}`, "role required"},
	}
	for _, tc := range cases {
		_, err := Get("workspace_member_add").Invoke(context.Background(), json.RawMessage(tc.body))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("body=%s: expected %q, got %v", tc.body, tc.want, err)
		}
	}
}

func TestWorkspaceMemberList_RequiresID(t *testing.T) {
	prev := workspaceStore
	defer func() { workspaceStore = prev }()
	workspaceStore = &workspace.Store{}
	_, err := Get("workspace_member_list").Invoke(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "workspace_id required") {
		t.Fatalf("expected workspace_id-required, got %v", err)
	}
}
