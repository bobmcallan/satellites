package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"

	satarbor "github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/workspace"
)

// newDocumentTestServer builds a Server with MemoryStore-backed
// dependencies for handler-level unit tests.
func newDocumentTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := &config.Config{Env: "dev"}
	wsStore := workspace.NewMemoryStore()
	docStore := document.NewMemoryStore()
	return New(cfg, satarbor.New("info"), time.Now(), Deps{
		DocStore:       docStore,
		WorkspaceStore: wsStore,
	})
}

// withCaller wraps ctx with the supplied identity so handler calls see
// the caller as if AuthMiddleware had run.
func withCaller(ctx context.Context, id CallerIdentity) context.Context {
	return context.WithValue(ctx, userKey, id)
}

func newCallToolReq(name string, args map[string]any) mcpgo.CallToolRequest {
	req := mcpgo.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	return req
}

// TestHandleDocumentUpdate_RejectsImmutable enumerates every immutable
// field and confirms the handler returns isError naming the offending
// field.
func TestHandleDocumentUpdate_RejectsImmutable(t *testing.T) {
	t.Parallel()
	s := newDocumentTestServer(t)
	ctx := withCaller(context.Background(), CallerIdentity{UserID: "u_a", Source: "session"})

	// Seed a row to update.
	doc, err := s.docs.Create(ctx, document.Document{
		Type:  document.TypePrinciple,
		Scope: document.ScopeSystem,
		Name:  "p",
		Tags:  []string{"v4"},
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	cases := []string{"workspace_id", "project_id", "type", "scope", "name"}
	for _, field := range cases {
		t.Run(field, func(t *testing.T) {
			res, err := s.handleDocumentUpdate(ctx, newCallToolReq("document_update", map[string]any{
				"id":  doc.ID,
				field: "tampered",
			}))
			if err != nil {
				t.Fatalf("handler error: %v", err)
			}
			text := firstText(res)
			if !strings.Contains(text, "immutable field rejected: "+field) {
				t.Errorf("rejection text = %q, want to mention %q", text, field)
			}
			if !res.IsError {
				t.Errorf("IsError = false; want true for immutable %q", field)
			}
		})
	}
}

// TestHandleDocumentList_WorkspaceIsolation builds two workspaces with
// distinct callers, each owning a row; each caller's document_list must
// see only their own row.
func TestHandleDocumentList_WorkspaceIsolation(t *testing.T) {
	t.Parallel()
	s := newDocumentTestServer(t)
	ctx := context.Background()

	// Mint two workspaces, each owned by a distinct user.
	wsA, err := s.workspaces.Create(ctx, "user_alice", "alpha", time.Now().UTC())
	if err != nil {
		t.Fatalf("ws A: %v", err)
	}
	wsB, err := s.workspaces.Create(ctx, "user_bob", "beta", time.Now().UTC())
	if err != nil {
		t.Fatalf("ws B: %v", err)
	}
	// Seed one principle per workspace directly via the store.
	if _, err := s.docs.Create(ctx, document.Document{
		WorkspaceID: wsA.ID,
		Type:        document.TypePrinciple,
		Scope:       document.ScopeSystem,
		Name:        "alice-only",
	}, time.Now().UTC()); err != nil {
		t.Fatalf("alice principle: %v", err)
	}
	if _, err := s.docs.Create(ctx, document.Document{
		WorkspaceID: wsB.ID,
		Type:        document.TypePrinciple,
		Scope:       document.ScopeSystem,
		Name:        "bob-only",
	}, time.Now().UTC()); err != nil {
		t.Fatalf("bob principle: %v", err)
	}

	aliceCtx := withCaller(ctx, CallerIdentity{UserID: "user_alice", Source: "session"})
	bobCtx := withCaller(ctx, CallerIdentity{UserID: "user_bob", Source: "session"})

	resA, _ := s.handleDocumentList(aliceCtx, newCallToolReq("document_list", map[string]any{"type": "principle"}))
	resB, _ := s.handleDocumentList(bobCtx, newCallToolReq("document_list", map[string]any{"type": "principle"}))
	rowsA := decodeArray(t, resA)
	rowsB := decodeArray(t, resB)

	if len(rowsA) != 1 || nameOf(rowsA[0]) != "alice-only" {
		t.Errorf("alice list = %+v, want only alice-only", rowsA)
	}
	if len(rowsB) != 1 || nameOf(rowsB[0]) != "bob-only" {
		t.Errorf("bob list = %+v, want only bob-only", rowsB)
	}
}

// TestHandleDocumentCreate_ScopeSystemRejectsProjectID confirms the
// scope-vs-project-id invariant is enforced at the handler layer (not
// only in document.Validate at the store layer).
func TestHandleDocumentCreate_ScopeSystemRejectsProjectID(t *testing.T) {
	t.Parallel()
	s := newDocumentTestServer(t)
	ctx := withCaller(context.Background(), CallerIdentity{UserID: "u_a", Source: "session"})

	res, err := s.handleDocumentCreate(ctx, newCallToolReq("document_create", map[string]any{
		"type":       "principle",
		"scope":      "system",
		"name":       "bad",
		"project_id": "proj_x",
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Errorf("scope=system + project_id should isError; got %s", firstText(res))
	}
}

func firstText(res *mcpgo.CallToolResult) string {
	if res == nil || len(res.Content) == 0 {
		return ""
	}
	if t, ok := res.Content[0].(mcpgo.TextContent); ok {
		return t.Text
	}
	return ""
}

func decodeArray(t *testing.T, res *mcpgo.CallToolResult) []map[string]any {
	t.Helper()
	if res == nil || res.IsError {
		t.Fatalf("isError or nil: %+v", res)
	}
	text := firstText(res)
	if text == "" || text == "null" {
		return nil
	}
	var out []map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode array: %v; raw=%s", err, text)
	}
	return out
}

func nameOf(row map[string]any) string {
	v, _ := row["name"].(string)
	return v
}
