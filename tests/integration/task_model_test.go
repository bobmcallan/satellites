//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestTaskModel exercises sty_8a83d378 — documents.type='task' as the
// third discriminator alongside 'document' and 'story'. Creates a
// story, hangs two tasks off it via document_upsert(type='task'),
// then lists the tasks via document_list(type='task').
//
// Catches:
//   - migration 0018's type CHECK relaxation (else the insert fails)
//   - createTask path in verb.invokeDocumentUpsert
//   - document_list type='task' filter
//   - parent_id semantic constraint (task can only hang off a story)
func TestTaskModel(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	authStore := auth.New(env.DB)
	if err := authStore.DevSeed(context.Background()); err != nil {
		t.Fatalf("dev seed: %v", err)
	}
	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	docStore := document.New(env.DB)
	verb.SetAuthStore(authStore)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	verb.SetDocumentStore(docStore)
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
		verb.SetDocumentStore(nil)
	})

	ctx := context.Background()
	now := time.Date(2026, 5, 27, 10, 0, 0, 0, time.UTC)
	ws, err := wsStore.Create(ctx, "", "task-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{
		WorkspaceID: ws.ID,
		Name:        "task-project",
	}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	// Parent story.
	storyReq, _ := json.Marshal(verb.DocumentUpsertRequest{
		Type:      "story",
		ProjectID: pj.ID,
		Name:      "parent story for tasks",
	})
	storyRaw, err := verb.Dispatch(ctx, "document_upsert", storyReq)
	if err != nil {
		t.Fatalf("create story: %v", err)
	}
	var story verb.DocumentUpsertResponse
	if err := json.Unmarshal(storyRaw, &story); err != nil {
		t.Fatalf("decode story: %v", err)
	}
	if story.Document.ID == "" {
		t.Fatal("story id empty")
	}

	// Create two tasks under the story.
	taskIDs := make([]string, 0, 2)
	for _, title := range []string{"plan iteration 1", "execute iteration 1"} {
		parent := story.Document.ID
		req, _ := json.Marshal(verb.DocumentUpsertRequest{
			Type:      "task",
			ProjectID: pj.ID,
			Name:      title,
			ParentID:  &parent,
		})
		raw, err := verb.Dispatch(ctx, "document_upsert", req)
		if err != nil {
			t.Fatalf("create task %q: %v", title, err)
		}
		var task verb.DocumentUpsertResponse
		if err := json.Unmarshal(raw, &task); err != nil {
			t.Fatalf("decode task %q: %v", title, err)
		}
		if !strings.HasPrefix(task.Document.ID, "tsk_") {
			t.Errorf("task %q id %q does not start with tsk_", title, task.Document.ID)
		}
		if task.Document.Type != "task" {
			t.Errorf("task %q type %q, want task", title, task.Document.Type)
		}
		if task.Document.ParentID != story.Document.ID {
			t.Errorf("task %q parent_id %q, want %q", title, task.Document.ParentID, story.Document.ID)
		}
		taskIDs = append(taskIDs, task.Document.ID)
	}

	// List tasks under the project.
	listReq, _ := json.Marshal(verb.DocumentListRequest{
		Type:      "task",
		ProjectID: pj.ID,
	})
	listRaw, err := verb.Dispatch(ctx, "document_list", listReq)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	var listResp verb.DocumentListResponse
	if err := json.Unmarshal(listRaw, &listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listResp.Items) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(listResp.Items))
	}
	gotIDs := make(map[string]bool, len(listResp.Items))
	for _, d := range listResp.Items {
		if d.Type != "task" {
			t.Errorf("list returned non-task type %q for id %s", d.Type, d.ID)
		}
		if d.ParentID != story.Document.ID {
			t.Errorf("list returned task %s with parent_id %q, want %q", d.ID, d.ParentID, story.Document.ID)
		}
		gotIDs[d.ID] = true
	}
	for _, want := range taskIDs {
		if !gotIDs[want] {
			t.Errorf("list missing task %s", want)
		}
	}

	// document_list with type='story' must NOT include tasks.
	storyListReq, _ := json.Marshal(verb.DocumentListRequest{
		Type:      "story",
		ProjectID: pj.ID,
	})
	storyListRaw, err := verb.Dispatch(ctx, "document_list", storyListReq)
	if err != nil {
		t.Fatalf("list stories: %v", err)
	}
	var storyListResp verb.DocumentListResponse
	if err := json.Unmarshal(storyListRaw, &storyListResp); err != nil {
		t.Fatalf("decode story list: %v", err)
	}
	for _, d := range storyListResp.Items {
		if d.Type != "story" {
			t.Errorf("story list leaked type=%q id=%s", d.Type, d.ID)
		}
	}

	// Parent-must-be-story constraint: try to create a task whose
	// parent is another task. Should fail.
	otherTaskParent := taskIDs[0]
	badParentReq, _ := json.Marshal(verb.DocumentUpsertRequest{
		Type:      "task",
		ProjectID: pj.ID,
		Name:      "task with task parent",
		ParentID:  &otherTaskParent,
	})
	if _, err := verb.Dispatch(ctx, "document_upsert", badParentReq); err == nil {
		t.Error("creating a task with task-typed parent_id should fail; got nil error")
	} else if !strings.Contains(err.Error(), "parent must be a story") {
		t.Errorf("error %q should mention parent-must-be-story constraint", err.Error())
	}

	// Missing parent_id rejected at the verb layer.
	noParentReq, _ := json.Marshal(verb.DocumentUpsertRequest{
		Type:      "task",
		ProjectID: pj.ID,
		Name:      "task without parent",
	})
	if _, err := verb.Dispatch(ctx, "document_upsert", noParentReq); err == nil {
		t.Error("creating a task without parent_id should fail; got nil error")
	} else if !strings.Contains(err.Error(), "parent_id") {
		t.Errorf("error %q should mention parent_id requirement", err.Error())
	}
}
