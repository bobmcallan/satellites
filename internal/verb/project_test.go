package verb

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/project"
)

func TestProjectVerbs_Registered(t *testing.T) {
	want := []string{"project_create", "project_list", "project_get", "project_update", "project_match"}
	for _, name := range want {
		if Get(name) == nil {
			t.Errorf("verb %q not registered", name)
		}
	}
}

func TestProjectCreate_StoreNotConfigured(t *testing.T) {
	prev := projectStore
	projectStore = nil
	defer func() { projectStore = prev }()

	_, err := Get("project_create").Invoke(context.Background(),
		json.RawMessage(`{"name":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "store not configured") {
		t.Fatalf("expected store-not-configured error, got %v", err)
	}
}

func TestProjectCreate_RequiresName(t *testing.T) {
	prev := projectStore
	defer func() { projectStore = prev }()
	projectStore = &project.Store{}

	for _, body := range []string{`{}`, `{"name":""}`, `{"name":"   "}`} {
		_, err := Get("project_create").Invoke(context.Background(),
			json.RawMessage(body))
		if err == nil || !strings.Contains(err.Error(), "name required") {
			t.Fatalf("body=%s: expected name-required error, got %v", body, err)
		}
	}
}

func TestProjectGet_RequiresID(t *testing.T) {
	prev := projectStore
	defer func() { projectStore = prev }()
	projectStore = &project.Store{}

	_, err := Get("project_get").Invoke(context.Background(),
		json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "id required") {
		t.Fatalf("expected id-required error, got %v", err)
	}
}

func TestProjectUpdate_RequiresID(t *testing.T) {
	prev := projectStore
	defer func() { projectStore = prev }()
	projectStore = &project.Store{}

	_, err := Get("project_update").Invoke(context.Background(),
		json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "id required") {
		t.Fatalf("expected id-required error, got %v", err)
	}
}

func TestProjectMatch_StoreNotConfigured(t *testing.T) {
	prev := projectStore
	projectStore = nil
	defer func() { projectStore = prev }()

	_, err := Get("project_match").Invoke(context.Background(),
		json.RawMessage(`{"git_url":"git@github.com:owner/repo.git"}`))
	if err == nil || !strings.Contains(err.Error(), "store not configured") {
		t.Fatalf("expected store-not-configured error, got %v", err)
	}
}

func TestProjectMatch_RequiresGitURL(t *testing.T) {
	prev := projectStore
	defer func() { projectStore = prev }()
	projectStore = &project.Store{}

	for _, body := range []string{`{}`, `{"git_url":""}`, `{"git_url":"   "}`} {
		_, err := Get("project_match").Invoke(context.Background(),
			json.RawMessage(body))
		if err == nil || !strings.Contains(err.Error(), "git_url required") {
			t.Fatalf("body=%s: expected git_url-required error, got %v", body, err)
		}
	}
}

// TestFilterProjectsByTags pins the AND-semantics, exact-element tag filter
// project_list applies (sty_87379afa) — the in-memory mirror of document_list's
// `tags @> …`. A KV tag like phase:discovery must not match phase:discovery-notes.
func TestFilterProjectsByTags(t *testing.T) {
	ps := []project.Project{
		{ID: "a", Tags: []string{"type:document", "phase:discovery"}},
		{ID: "b", Tags: []string{"type:diagram", "phase:discovery-notes"}},
		{ID: "c", Tags: []string{"phase:build"}},
		{ID: "d", Tags: nil},
	}
	cases := []struct {
		name string
		want []string
		ids  []string
	}{
		{"phase discovery exact", []string{"phase:discovery"}, []string{"a"}},
		{"classification", []string{"type:document"}, []string{"a"}},
		{"phase build", []string{"phase:build"}, []string{"c"}},
		{"AND of two tags", []string{"type:document", "phase:discovery"}, []string{"a"}},
		{"AND misses when one absent", []string{"type:document", "phase:build"}, nil},
		{"no false prefix match", []string{"phase:discovery"}, []string{"a"}}, // not b
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filterProjectsByTags(ps, tc.want)
			var ids []string
			for _, p := range got {
				ids = append(ids, p.ID)
			}
			if strings.Join(ids, ",") != strings.Join(tc.ids, ",") {
				t.Errorf("filter %v = %v, want %v", tc.want, ids, tc.ids)
			}
		})
	}
}
