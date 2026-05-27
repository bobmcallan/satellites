package verb

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/changelog"
)

func TestChangelogVerbs_Registered(t *testing.T) {
	for _, name := range []string{
		"changelog_add", "changelog_list", "changelog_update", "changelog_delete",
	} {
		if Get(name) == nil {
			t.Errorf("verb %q not registered", name)
		}
	}
}

func TestChangelogAdd_StoreNotConfigured(t *testing.T) {
	prev := changelogStore
	changelogStore = nil
	defer func() { changelogStore = prev }()
	_, err := Get("changelog_add").Invoke(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "changelog store not configured") {
		t.Fatalf("expected store-not-configured error, got %v", err)
	}
}

func TestChangelogAdd_RequiresAuth(t *testing.T) {
	prev := changelogStore
	changelogStore = &changelog.Store{}
	defer func() { changelogStore = prev }()
	_, err := Get("changelog_add").Invoke(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "unauthenticated") {
		t.Fatalf("expected unauthenticated error, got %v", err)
	}
}

func TestChangelogUpdate_RequiresID(t *testing.T) {
	prev := changelogStore
	changelogStore = &changelog.Store{}
	defer func() { changelogStore = prev }()
	ctx := auth.WithUser(context.Background(), &auth.User{ID: "usr_test"})
	_, err := Get("changelog_update").Invoke(ctx, json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "id required") {
		t.Fatalf("expected id-required error, got %v", err)
	}
}

func TestChangelogDelete_RequiresID(t *testing.T) {
	prev := changelogStore
	changelogStore = &changelog.Store{}
	defer func() { changelogStore = prev }()
	ctx := auth.WithUser(context.Background(), &auth.User{ID: "usr_test"})
	_, err := Get("changelog_delete").Invoke(ctx, json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "id required") {
		t.Fatalf("expected id-required error, got %v", err)
	}
}

func TestChangelogList_AllowsUnauthenticated(t *testing.T) {
	// changelog_list is a public read — no auth required (the /changelog
	// page is open). Store-not-configured should be the only obstacle.
	prev := changelogStore
	changelogStore = nil
	defer func() { changelogStore = prev }()
	_, err := Get("changelog_list").Invoke(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "changelog store not configured") {
		t.Fatalf("expected store-not-configured error, got %v", err)
	}
}

func TestChangelogAdd_BadJSON(t *testing.T) {
	prev := changelogStore
	changelogStore = &changelog.Store{}
	defer func() { changelogStore = prev }()
	ctx := auth.WithUser(context.Background(), &auth.User{ID: "usr_test"})
	_, err := Get("changelog_add").Invoke(ctx, json.RawMessage(`not-json`))
	if err == nil || !strings.Contains(err.Error(), "bad request") {
		t.Fatalf("expected bad-request error, got %v", err)
	}
}
