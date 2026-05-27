package verb

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/auth"
)

func TestAPIKeyVerbs_Registered(t *testing.T) {
	for _, name := range []string{"apikey_create", "apikey_list", "apikey_revoke"} {
		if Get(name) == nil {
			t.Errorf("verb %q not registered", name)
		}
	}
}

func TestAPIKeyCreate_StoreNotConfigured(t *testing.T) {
	prev := authStore
	authStore = nil
	defer func() { authStore = prev }()
	_, err := Get("apikey_create").Invoke(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "auth store not configured") {
		t.Fatalf("expected store-not-configured error, got %v", err)
	}
}

func TestAPIKeyCreate_RequiresAuth(t *testing.T) {
	prev := authStore
	defer func() { authStore = prev }()
	authStore = &auth.Store{}
	_, err := Get("apikey_create").Invoke(context.Background(), json.RawMessage(`{"workspace_id":"w1"}`))
	if err == nil || !strings.Contains(err.Error(), "unauthenticated") {
		t.Fatalf("expected unauthenticated error, got %v", err)
	}
}

func TestAPIKeyCreate_RequiresWorkspaceOrProject(t *testing.T) {
	prev := authStore
	defer func() { authStore = prev }()
	authStore = &auth.Store{}
	ctx := auth.WithUser(context.Background(), &auth.User{ID: "usr_test"})
	_, err := Get("apikey_create").Invoke(ctx, json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "workspace_id or project_id required") {
		t.Fatalf("expected required-id error, got %v", err)
	}
}

func TestAPIKeyList_RequiresAuth(t *testing.T) {
	prev := authStore
	defer func() { authStore = prev }()
	authStore = &auth.Store{}
	_, err := Get("apikey_list").Invoke(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "unauthenticated") {
		t.Fatalf("expected unauthenticated error, got %v", err)
	}
}

func TestAPIKeyRevoke_RequiresAuth(t *testing.T) {
	prev := authStore
	defer func() { authStore = prev }()
	authStore = &auth.Store{}
	_, err := Get("apikey_revoke").Invoke(context.Background(), json.RawMessage(`{"key_id":"apk_x"}`))
	if err == nil || !strings.Contains(err.Error(), "unauthenticated") {
		t.Fatalf("expected unauthenticated error, got %v", err)
	}
}

func TestAPIKeyRevoke_RequiresKeyID(t *testing.T) {
	prev := authStore
	defer func() { authStore = prev }()
	authStore = &auth.Store{}
	ctx := auth.WithUser(context.Background(), &auth.User{ID: "usr_test"})
	_, err := Get("apikey_revoke").Invoke(ctx, json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "key_id required") {
		t.Fatalf("expected key_id-required error, got %v", err)
	}
}
