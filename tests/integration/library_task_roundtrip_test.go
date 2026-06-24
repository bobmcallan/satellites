//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestLibraryTaskPublishRoundTrip exercises the verb-level publish→read path
// that `satellites task publish` + `task sync` depend on (sty_40baf67d): a
// library-scope task upserts (publish), is discoverable via document_list
// {type:task,scope:library} (sync's listLibraryTasks), and its body round-trips
// via document_get (sync's per-task body fetch). This is the full store-layer
// round-trip the global-tasks task path was missing — it now works in-process,
// so the deployed HTTP path works after ship.
func TestLibraryTaskPublishRoundTrip(t *testing.T) {
	env := testbootstrap.SetUp(t)
	wsStore := workspace.New(env.DB)
	projStore := project.New(env.DB)
	docStore := document.New(env.DB)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(projStore)
	verb.SetDocumentStore(docStore)
	verb.SetAuthStore(nil) // CLI-local caller: library write authz bypasses.
	t.Cleanup(func() {
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
		verb.SetDocumentStore(nil)
	})

	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	ws, err := wsStore.Create(ctx, "", "pub-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pub, err := projStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "publisher"}, now)
	if err != nil {
		t.Fatalf("create publisher: %v", err)
	}

	// Publish: the exact payload `task publish` dispatches.
	body := "# Codegraph\n\n## Task\n\nGenerate the repo's high-level codegraph.\n"
	pubReq, _ := json.Marshal(map[string]any{
		"type": "task", "scope": "library", "project_id": pub.ID, "name": "Codegraph", "body": body,
		"tags": []string{"workflow:satellites-task-workflow"},
	})
	if _, err := verb.Dispatch(ctx, "document_upsert", pubReq); err != nil {
		t.Fatalf("publish (document_upsert library task): %v", err)
	}

	// sync step 1: document_list {type:task, scope:library, project_id} must list it.
	listReq, _ := json.Marshal(map[string]any{"type": "task", "scope": "library", "project_id": pub.ID, "limit": 200})
	listRaw, err := verb.Dispatch(ctx, "document_list", listReq)
	if err != nil {
		t.Fatalf("document_list library tasks: %v", err)
	}
	var listed struct {
		Items []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"items"`
	}
	if err := json.Unmarshal(listRaw, &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	found := false
	for _, it := range listed.Items {
		if it.Name == "Codegraph" {
			found = true
		}
	}
	if !found {
		t.Fatalf("document_list {type:task,scope:library} must return the published task; got %+v", listed.Items)
	}

	// sync step 2: document_get by (name, scope:library, project_id) round-trips the body.
	getReq, _ := json.Marshal(map[string]any{"name": "Codegraph", "scope": "library", "project_id": pub.ID})
	getRaw, err := verb.Dispatch(ctx, "document_get", getReq)
	if err != nil {
		t.Fatalf("document_get library task: %v", err)
	}
	var got struct {
		RawBody string `json:"raw_body"`
	}
	if err := json.Unmarshal(getRaw, &got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if !strings.Contains(got.RawBody, "Generate the repo's high-level codegraph") {
		t.Errorf("library task body did not round-trip via document_get: %q", got.RawBody)
	}
}
