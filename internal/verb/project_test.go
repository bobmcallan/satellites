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
