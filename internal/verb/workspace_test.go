package verb

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/workspace"
)

// nonNilWorkspaceStore returns a Store with a nil DB. Useful for
// exercising verb input-validation paths that fail before the verb
// touches the store.
func nonNilWorkspaceStore() *workspace.Store {
	return &workspace.Store{}
}

// Tests in this file exercise the request-validation + dispatcher
// surface of the workspace_* verbs. SQL-backed behaviour (uniqueness
// of is_default, scan semantics, etc.) lives behind a real Postgres
// and is exercised by the manual smoke documented in the plan file.

func TestWorkspaceVerbs_Registered(t *testing.T) {
	want := []string{"workspace_create", "workspace_list", "workspace_get", "workspace_set_default"}
	for _, name := range want {
		if Get(name) == nil {
			t.Errorf("verb %q not registered", name)
		}
	}
}

func TestWorkspaceCreate_StoreNotConfigured(t *testing.T) {
	prev := workspaceStore
	workspaceStore = nil
	defer func() { workspaceStore = prev }()

	v := Get("workspace_create")
	if v == nil {
		t.Fatal("workspace_create not registered")
	}
	_, err := v.Invoke(context.Background(), json.RawMessage(`{"name":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "store not configured") {
		t.Fatalf("expected store-not-configured error, got %v", err)
	}
}

func TestWorkspaceCreate_RequiresName(t *testing.T) {
	v := Get("workspace_create")
	// nil store path still rejects empty name before touching the
	// store IF we check after the store nil-guard. To exercise the
	// name-required branch cleanly, install a sentinel-store-nil and
	// rely on the verb's name check.
	prev := workspaceStore
	defer func() { workspaceStore = prev }()
	// With workspaceStore == nil, the store-not-configured error
	// trumps the name check. Install a non-nil placeholder by
	// constructing a Store value directly (it never gets called
	// because the name check fires first).
	workspaceStore = nonNilWorkspaceStore()

	cases := []struct {
		name string
		raw  string
	}{
		{"empty json", `{}`},
		{"empty name", `{"name":""}`},
		{"whitespace name", `{"name":"   "}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := v.Invoke(context.Background(), json.RawMessage(tc.raw))
			if err == nil || !strings.Contains(err.Error(), "name required") {
				t.Fatalf("expected name-required error, got %v", err)
			}
		})
	}
}

func TestWorkspaceGet_RequiresID(t *testing.T) {
	prev := workspaceStore
	defer func() { workspaceStore = prev }()
	workspaceStore = nonNilWorkspaceStore()

	v := Get("workspace_get")
	_, err := v.Invoke(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "id required") {
		t.Fatalf("expected id-required error, got %v", err)
	}
}

func TestWorkspaceSetDefault_RequiresID(t *testing.T) {
	prev := workspaceStore
	defer func() { workspaceStore = prev }()
	workspaceStore = nonNilWorkspaceStore()

	v := Get("workspace_set_default")
	_, err := v.Invoke(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "id required") {
		t.Fatalf("expected id-required error, got %v", err)
	}
}

func TestWorkspaceCreate_BadJSON(t *testing.T) {
	prev := workspaceStore
	defer func() { workspaceStore = prev }()
	workspaceStore = nonNilWorkspaceStore()

	v := Get("workspace_create")
	_, err := v.Invoke(context.Background(), json.RawMessage(`{"name":`))
	if err == nil || !strings.Contains(err.Error(), "bad request") {
		t.Fatalf("expected bad-request error, got %v", err)
	}
}
