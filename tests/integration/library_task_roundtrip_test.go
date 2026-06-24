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

// TestLibraryTaskDiscoverCopyRoundTrip is the sprint dogfood at the verb level
// (sty_164bf65e): it proves the discovery + adopt round-trip the new surfaces
// rely on — a published library task is discoverable by a GLOBAL document_list
// {type:task, scope:library} with NO project_id (what `task library list` and the
// library page issue), materialises into a CONSUMER project as a live forked
// tsk_ row (what `task copy` does), and a re-copy reconciles that row in place
// rather than minting a duplicate.
func TestLibraryTaskDiscoverCopyRoundTrip(t *testing.T) {
	env := testbootstrap.SetUp(t)
	wsStore := workspace.New(env.DB)
	projStore := project.New(env.DB)
	docStore := document.New(env.DB)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(projStore)
	verb.SetDocumentStore(docStore)
	verb.SetAuthStore(nil)
	t.Cleanup(func() {
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
		verb.SetDocumentStore(nil)
	})

	ctx := context.Background()
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	ws, err := wsStore.Create(ctx, "", "sprint-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pub, err := projStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "publisher"}, now)
	if err != nil {
		t.Fatalf("create publisher: %v", err)
	}
	consumer, err := projStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "consumer"}, now)
	if err != nil {
		t.Fatalf("create consumer: %v", err)
	}

	// Publish a library task.
	body := "# Codegraph\n\n## Task\n\nGenerate the repo's high-level codegraph.\n"
	pubReq, _ := json.Marshal(map[string]any{
		"type": "task", "scope": "library", "project_id": pub.ID, "name": "Codegraph", "body": body,
		"tags": []string{"workflow:satellites-task-workflow"},
	})
	if _, err := verb.Dispatch(ctx, "document_upsert", pubReq); err != nil {
		t.Fatalf("publish library task: %v", err)
	}

	// Discover: a GLOBAL list (no project_id) returns the library task — the
	// contract `task library list` + the library page depend on. A scope:library
	// request keeps library rows that dropForeignLibraryTasks hides from
	// project-scope listings.
	globalReq, _ := json.Marshal(map[string]any{"type": "task", "scope": "library", "limit": 500})
	globalRaw, err := verb.Dispatch(ctx, "document_list", globalReq)
	if err != nil {
		t.Fatalf("global library list: %v", err)
	}
	if !listHasTask(t, globalRaw, "Codegraph", pub.ID) {
		t.Fatalf("global document_list {type:task,scope:library} must surface the published task")
	}

	// Copy: materialise into the consumer as a live forked project task (what
	// upsertConsumedTask dispatches — scope:project, no id mints a tsk_).
	src := pub.ID + "/Codegraph"
	copyReq, _ := json.Marshal(map[string]any{
		"type": "task", "scope": "project", "project_id": consumer.ID,
		"name": "Codegraph", "body": body, "tags": []string{"forked-from:" + src},
	})
	if _, err := verb.Dispatch(ctx, "document_upsert", copyReq); err != nil {
		t.Fatalf("copy (materialise) into consumer: %v", err)
	}

	// The consumer now has exactly one live forked Codegraph task.
	id, count := consumerForkedTask(t, ctx, consumer.ID, "Codegraph", src)
	if count != 1 || id == "" {
		t.Fatalf("want one live forked tsk_ in consumer, got count=%d id=%q", count, id)
	}

	// Re-copy reconciles the SAME row by id — no duplicate.
	reReq, _ := json.Marshal(map[string]any{
		"id": id, "type": "task", "scope": "project", "project_id": consumer.ID,
		"name": "Codegraph", "body": body + "\n(updated)\n", "tags": []string{"forked-from:" + src},
	})
	if _, err := verb.Dispatch(ctx, "document_upsert", reReq); err != nil {
		t.Fatalf("re-copy (reconcile by id): %v", err)
	}
	id2, count2 := consumerForkedTask(t, ctx, consumer.ID, "Codegraph", src)
	if count2 != 1 || id2 != id {
		t.Fatalf("re-copy must reconcile in place (no duplicate): want id=%q count=1, got id=%q count=%d", id, id2, count2)
	}
}

// listHasTask reports whether a document_list response contains a task of the
// given name (and publisher, when projectID != "").
func listHasTask(t *testing.T, raw []byte, name, projectID string) bool {
	t.Helper()
	var listed struct {
		Items []struct {
			Name      string `json:"name"`
			ProjectID string `json:"project_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	for _, it := range listed.Items {
		if it.Name == name && (projectID == "" || it.ProjectID == projectID) {
			return true
		}
	}
	return false
}

// consumerForkedTask returns the id and count of forked tasks in a project that
// match name + the forked-from source — proving a live, deduplicated copy.
func consumerForkedTask(t *testing.T, ctx context.Context, projectID, name, src string) (string, int) {
	t.Helper()
	listReq, _ := json.Marshal(map[string]any{"type": "task", "project_id": projectID, "limit": 500})
	raw, err := verb.Dispatch(ctx, "document_list", listReq)
	if err != nil {
		t.Fatalf("list consumer tasks: %v", err)
	}
	var listed struct {
		Items []struct {
			ID   string   `json:"id"`
			Name string   `json:"name"`
			Tags []string `json:"tags"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatalf("decode consumer list: %v", err)
	}
	id, count := "", 0
	for _, it := range listed.Items {
		if it.Name != name {
			continue
		}
		for _, tg := range it.Tags {
			if tg == "forked-from:"+src {
				id, count = it.ID, count+1
			}
		}
	}
	return id, count
}
