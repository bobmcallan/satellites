package mcpserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	satarbor "github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/session"
	"github.com/bobmcallan/satellites/internal/story"
	"github.com/bobmcallan/satellites/internal/workspace"
)

// newKVTestServer wires the dependencies the unified KV verbs need:
// the ledger store (for read+write), workspaces (for membership
// resolution), projects + stories + contracts + docs (so the
// registration site at mcp.go:376 advertises the kv_* tools).
func newKVTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := &config.Config{Env: "dev"}
	now := time.Date(2026, 4, 28, 22, 0, 0, 0, time.UTC)
	ledStore := ledger.NewMemoryStore()
	docStore := document.NewMemoryStore()
	storyStore := story.NewMemoryStore(ledStore)
	projStore := project.NewMemoryStore()
	contractStore := contract.NewMemoryStore(docStore, storyStore)
	wsStore := workspace.NewMemoryStore()
	sessionStore := session.NewMemoryStore()
	return New(cfg, satarbor.New("info"), now, Deps{
		DocStore:       docStore,
		ProjectStore:   projStore,
		LedgerStore:    ledStore,
		StoryStore:     storyStore,
		ContractStore:  contractStore,
		WorkspaceStore: wsStore,
		SessionStore:   sessionStore,
	})
}

// TestKVTools_Registered confirms the four kv_* MCP tools land in the
// ListTools surface when the registration site's deps are wired.
func TestKVTools_Registered(t *testing.T) {
	t.Parallel()
	s := newKVTestServer(t)
	tools := s.mcp.ListTools()
	for _, want := range []string{"kv_get", "kv_set", "kv_delete", "kv_list"} {
		if _, ok := tools[want]; !ok {
			t.Errorf("registered tools missing %q (story_3d392258)", want)
		}
	}
}

func TestKVHandlers_RoundTrip_System(t *testing.T) {
	t.Parallel()
	s := newKVTestServer(t)
	admin := CallerIdentity{UserID: "u_admin", Source: "session", GlobalAdmin: true}
	ctx := withCaller(context.Background(), admin)

	// kv_set
	setRes, err := s.handleKVSet(ctx, newCallToolReq("kv_set", map[string]any{
		"scope": "system",
		"key":   "default_theme",
		"value": "dark",
	}))
	if err != nil {
		t.Fatalf("kv_set: %v", err)
	}
	if setRes.IsError {
		t.Fatalf("kv_set IsError: %s", firstText(setRes))
	}

	// kv_get
	getRes, err := s.handleKVGet(ctx, newCallToolReq("kv_get", map[string]any{
		"scope": "system",
		"key":   "default_theme",
	}))
	if err != nil {
		t.Fatalf("kv_get: %v", err)
	}
	if getRes.IsError {
		t.Fatalf("kv_get IsError: %s", firstText(getRes))
	}
	var got map[string]any
	_ = json.Unmarshal([]byte(firstText(getRes)), &got)
	if got["value"] != "dark" {
		t.Errorf("kv_get value = %v, want dark", got["value"])
	}

	// kv_delete
	delRes, err := s.handleKVDelete(ctx, newCallToolReq("kv_delete", map[string]any{
		"scope": "system",
		"key":   "default_theme",
	}))
	if err != nil {
		t.Fatalf("kv_delete: %v", err)
	}
	if delRes.IsError {
		t.Fatalf("kv_delete IsError: %s", firstText(delRes))
	}

	// kv_get again — must be not_found
	getRes2, _ := s.handleKVGet(ctx, newCallToolReq("kv_get", map[string]any{
		"scope": "system",
		"key":   "default_theme",
	}))
	if !getRes2.IsError {
		t.Errorf("kv_get after delete: IsError = false, want true")
	}
}

func TestKVHandlers_RoundTrip_Workspace(t *testing.T) {
	t.Parallel()
	s := newKVTestServer(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// Seed a workspace + member so memberships resolve.
	ws, err := s.workspaces.Create(ctx, "u_alice", "alpha", now)
	if err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if err := s.workspaces.AddMember(ctx, ws.ID, "u_alice", workspace.RoleAdmin, "u_alice", now); err != nil {
		t.Fatalf("seed member: %v", err)
	}

	alice := CallerIdentity{UserID: "u_alice", Source: "session"}
	ctx = withCaller(ctx, alice)

	setRes, err := s.handleKVSet(ctx, newCallToolReq("kv_set", map[string]any{
		"scope":        "workspace",
		"workspace_id": ws.ID,
		"key":          "tier",
		"value":        "gold",
	}))
	if err != nil || setRes.IsError {
		t.Fatalf("kv_set: %v / %s", err, firstText(setRes))
	}
	listRes, err := s.handleKVList(ctx, newCallToolReq("kv_list", map[string]any{
		"scope":        "workspace",
		"workspace_id": ws.ID,
	}))
	if err != nil || listRes.IsError {
		t.Fatalf("kv_list: %v / %s", err, firstText(listRes))
	}
	var listed map[string]any
	_ = json.Unmarshal([]byte(firstText(listRes)), &listed)
	if c, ok := listed["count"].(float64); !ok || c != 1 {
		t.Errorf("workspace kv_list count = %v, want 1", listed["count"])
	}
}

func TestKVHandlers_RoundTrip_User(t *testing.T) {
	t.Parallel()
	s := newKVTestServer(t)
	ctx := context.Background()
	now := time.Now().UTC()
	ws, _ := s.workspaces.Create(ctx, "u_alice", "alpha", now)
	_ = s.workspaces.AddMember(ctx, ws.ID, "u_alice", workspace.RoleAdmin, "u_alice", now)

	alice := CallerIdentity{UserID: "u_alice", Source: "session"}
	ctx = withCaller(ctx, alice)

	setRes, _ := s.handleKVSet(ctx, newCallToolReq("kv_set", map[string]any{
		"scope":        "user",
		"workspace_id": ws.ID,
		"key":          "theme",
		"value":        "dark",
	}))
	if setRes.IsError {
		t.Fatalf("kv_set: %s", firstText(setRes))
	}
	getRes, _ := s.handleKVGet(ctx, newCallToolReq("kv_get", map[string]any{
		"scope":        "user",
		"workspace_id": ws.ID,
		"key":          "theme",
	}))
	if getRes.IsError {
		t.Fatalf("kv_get: %s", firstText(getRes))
	}
	var got map[string]any
	_ = json.Unmarshal([]byte(firstText(getRes)), &got)
	if got["value"] != "dark" {
		t.Errorf("user kv_get value = %v, want dark", got["value"])
	}
}

func TestKVHandlers_InvalidScope(t *testing.T) {
	t.Parallel()
	s := newKVTestServer(t)
	ctx := withCaller(context.Background(), CallerIdentity{UserID: "u_a", Source: "session"})

	res, _ := s.handleKVGet(ctx, newCallToolReq("kv_get", map[string]any{
		"scope": "global",
		"key":   "x",
	}))
	if !res.IsError {
		t.Fatal("expected error for invalid scope")
	}
}

func TestKVHandlers_SystemSetRequiresGlobalAdmin(t *testing.T) {
	t.Parallel()
	s := newKVTestServer(t)
	ctx := withCaller(context.Background(), CallerIdentity{UserID: "u_a", Source: "session", GlobalAdmin: false})

	res, _ := s.handleKVSet(ctx, newCallToolReq("kv_set", map[string]any{
		"scope": "system",
		"key":   "policy",
		"value": "strict",
	}))
	if !res.IsError {
		t.Fatal("expected forbidden for non-admin scope=system write")
	}
}

func TestKVHandlers_WorkspaceFKMissing(t *testing.T) {
	t.Parallel()
	s := newKVTestServer(t)
	ctx := withCaller(context.Background(), CallerIdentity{UserID: "u_a", Source: "session"})

	res, _ := s.handleKVSet(ctx, newCallToolReq("kv_set", map[string]any{
		"scope": "workspace",
		"key":   "x",
		"value": "y",
	}))
	if !res.IsError {
		t.Fatal("expected error when workspace_id is missing for scope=workspace")
	}
}
