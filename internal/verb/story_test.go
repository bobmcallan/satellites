package verb

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/story"
)

func TestStoryVerbs_Registered(t *testing.T) {
	want := []string{"story_create", "story_list", "story_get", "story_update", "story_delete"}
	for _, name := range want {
		if Get(name) == nil {
			t.Errorf("verb %q not registered", name)
		}
	}
}

func TestStoryCreate_StoreNotConfigured(t *testing.T) {
	prev := storyStore
	storyStore = nil
	defer func() { storyStore = prev }()
	_, err := Get("story_create").Invoke(context.Background(),
		json.RawMessage(`{"project_id":"proj_x","title":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "store not configured") {
		t.Fatalf("expected store-not-configured error, got %v", err)
	}
}

func TestStoryCreate_RequiresProjectAndTitle(t *testing.T) {
	prev := storyStore
	defer func() { storyStore = prev }()
	storyStore = &story.Store{}

	cases := []struct {
		body string
		want string
	}{
		{`{}`, "project_id required"},
		{`{"project_id":"proj_x"}`, "title required"},
		{`{"project_id":"   ","title":"x"}`, "project_id required"},
		{`{"project_id":"proj_x","title":"  "}`, "title required"},
	}
	for _, tc := range cases {
		_, err := Get("story_create").Invoke(context.Background(),
			json.RawMessage(tc.body))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("body=%s: expected %q, got %v", tc.body, tc.want, err)
		}
	}
}

func TestStoryList_RequiresProjectID(t *testing.T) {
	prev := storyStore
	defer func() { storyStore = prev }()
	storyStore = &story.Store{}
	_, err := Get("story_list").Invoke(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "project_id required") {
		t.Fatalf("expected project_id-required error, got %v", err)
	}
}

func TestStoryGet_RequiresID(t *testing.T) {
	prev := storyStore
	defer func() { storyStore = prev }()
	storyStore = &story.Store{}
	_, err := Get("story_get").Invoke(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "id required") {
		t.Fatalf("expected id-required error, got %v", err)
	}
}

func TestStoryUpdate_RequiresID(t *testing.T) {
	prev := storyStore
	defer func() { storyStore = prev }()
	storyStore = &story.Store{}
	_, err := Get("story_update").Invoke(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "id required") {
		t.Fatalf("expected id-required error, got %v", err)
	}
}

func TestStoryDelete_StoreNotConfigured(t *testing.T) {
	prev := storyStore
	storyStore = nil
	defer func() { storyStore = prev }()
	_, err := Get("story_delete").Invoke(context.Background(),
		json.RawMessage(`{"id":"sty_x"}`))
	if err == nil || !strings.Contains(err.Error(), "store not configured") {
		t.Fatalf("expected store-not-configured error, got %v", err)
	}
}

func TestStoryDelete_RequiresID(t *testing.T) {
	prev := storyStore
	defer func() { storyStore = prev }()
	storyStore = &story.Store{}
	_, err := Get("story_delete").Invoke(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "id required") {
		t.Fatalf("expected id-required error, got %v", err)
	}
}
