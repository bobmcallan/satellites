package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// fakeTaskDispatch records document_upsert calls and answers the list/get reads
// reconcileTasks makes, so the create/update/skip verdicts can be asserted
// without a server.
type fakeTaskDispatch struct {
	libTasks  map[string]string // name → body (the publisher's library tasks)
	projTasks []projTask        // existing tasks in the consuming project
	upserts   []map[string]any  // every document_upsert payload, in order
}

func (f *fakeTaskDispatch) dispatch(_ context.Context, name string, req json.RawMessage) (json.RawMessage, error) {
	var m map[string]any
	_ = json.Unmarshal(req, &m)
	switch name {
	case "document_list":
		scope, _ := m["scope"].(string)
		if scope == "library" {
			items := []map[string]any{}
			for n := range f.libTasks {
				items = append(items, map[string]any{"name": n})
			}
			return json.Marshal(map[string]any{"items": items})
		}
		// project-task list
		items := []map[string]any{}
		for _, pt := range f.projTasks {
			items = append(items, map[string]any{"id": pt.ID, "name": pt.Name, "tags": pt.Tags})
		}
		return json.Marshal(map[string]any{"items": items})
	case "document_get":
		n, _ := m["name"].(string)
		return json.Marshal(map[string]any{
			"raw_body": f.libTasks[n],
			"document": map[string]any{"latest_version": 1},
		})
	case "document_upsert":
		f.upserts = append(f.upserts, m)
		return json.Marshal(map[string]any{"document": map[string]any{"id": "tsk_new", "latest_version": 1}})
	}
	return json.RawMessage(`{}`), nil
}

func TestReconcileTasks_CreatesAbsentTask(t *testing.T) {
	f := &fakeTaskDispatch{libTasks: map[string]string{"codegraph": "# codegraph task body"}}
	var buf bytes.Buffer
	if err := reconcileTasks(context.Background(), &buf, f.dispatch, []string{"proj_pub"}, "proj_consumer", false); err != nil {
		t.Fatal(err)
	}
	if len(f.upserts) != 1 {
		t.Fatalf("want 1 upsert, got %d", len(f.upserts))
	}
	up := f.upserts[0]
	if up["type"] != "task" || up["scope"] != "project" || up["project_id"] != "proj_consumer" || up["name"] != "codegraph" {
		t.Fatalf("create payload wrong: %v", up)
	}
	if _, hasID := up["id"]; hasID {
		t.Error("a create must NOT carry an id (mints a fresh tsk_)")
	}
	tags, _ := up["tags"].([]any)
	if len(tags) != 1 || tags[0] != "forked-from:proj_pub/codegraph" {
		t.Errorf("want forked-from stamp, got tags %v", tags)
	}
	if !strings.Contains(buf.String(), "create") {
		t.Errorf("want a create line, got %q", buf.String())
	}
}

func TestReconcileTasks_ReconcilesForkedByID(t *testing.T) {
	f := &fakeTaskDispatch{
		libTasks: map[string]string{"codegraph": "# updated body"},
		projTasks: []projTask{
			{ID: "tsk_existing", Name: "codegraph", Tags: []string{"forked-from:proj_pub/codegraph"}, ForkedSrc: "proj_pub/codegraph"},
		},
	}
	var buf bytes.Buffer
	if err := reconcileTasks(context.Background(), &buf, f.dispatch, []string{"proj_pub"}, "proj_consumer", false); err != nil {
		t.Fatal(err)
	}
	if len(f.upserts) != 1 {
		t.Fatalf("want 1 upsert (update by id), got %d", len(f.upserts))
	}
	if f.upserts[0]["id"] != "tsk_existing" {
		t.Errorf("update must target the existing id, got %v", f.upserts[0]["id"])
	}
	if !strings.Contains(buf.String(), "update") {
		t.Errorf("want an update line, got %q", buf.String())
	}
}

func TestReconcileTasks_LocalTaskWinsPrecedence(t *testing.T) {
	f := &fakeTaskDispatch{
		libTasks: map[string]string{"codegraph": "# library body"},
		projTasks: []projTask{
			// A local, non-forked task with the same name — must NOT be overwritten.
			{ID: "tsk_local", Name: "codegraph", Tags: []string{"area:codeindex"}},
		},
	}
	var buf bytes.Buffer
	if err := reconcileTasks(context.Background(), &buf, f.dispatch, []string{"proj_pub"}, "proj_consumer", false); err != nil {
		t.Fatal(err)
	}
	if len(f.upserts) != 0 {
		t.Fatalf("a local task must win — want 0 upserts, got %d", len(f.upserts))
	}
	if !strings.Contains(buf.String(), "skip") {
		t.Errorf("want a skip line, got %q", buf.String())
	}
}

func TestReconcileTasks_DryRunDispatchesNothing(t *testing.T) {
	f := &fakeTaskDispatch{libTasks: map[string]string{"codegraph": "# body"}}
	var buf bytes.Buffer
	if err := reconcileTasks(context.Background(), &buf, f.dispatch, []string{"proj_pub"}, "proj_consumer", true); err != nil {
		t.Fatal(err)
	}
	if len(f.upserts) != 0 {
		t.Fatalf("dry-run must dispatch no upsert, got %d", len(f.upserts))
	}
	if !strings.Contains(buf.String(), "[dry-run]") {
		t.Errorf("want a dry-run line, got %q", buf.String())
	}
}

func TestForkedFromValue(t *testing.T) {
	if got := forkedFromValue([]string{"area:x", "forked-from:proj_pub/codegraph"}); got != "proj_pub/codegraph" {
		t.Errorf("forkedFromValue = %q, want proj_pub/codegraph", got)
	}
	if got := forkedFromValue([]string{"area:x"}); got != "" {
		t.Errorf("forkedFromValue (local) = %q, want empty", got)
	}
}

func TestPublishableKind(t *testing.T) {
	for _, tc := range []struct {
		kind, typ, noun string
		ok              bool
	}{
		{"skills", "skill", "skill", true},
		{"tasks", "task", "task", true},
		{"documents", "", "", false},
	} {
		typ, noun, ok := publishableKind(tc.kind)
		if typ != tc.typ || noun != tc.noun || ok != tc.ok {
			t.Errorf("publishableKind(%q) = (%q,%q,%v), want (%q,%q,%v)", tc.kind, typ, noun, ok, tc.typ, tc.noun, tc.ok)
		}
	}
}
