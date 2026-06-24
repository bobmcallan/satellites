package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/verb"
)

// fakeTaskPublishDispatch answers the document_list (name→id), document_get
// (read the project task), and records the document_upsert (the library write)
// that publishProjectTask makes.
type fakeTaskPublishDispatch struct {
	id      string
	name    string
	typ     string
	body    string
	tags    []string
	upserts []map[string]any
}

func (f *fakeTaskPublishDispatch) dispatch(_ context.Context, name string, req json.RawMessage) (json.RawMessage, error) {
	switch name {
	case "document_list":
		return json.Marshal(map[string]any{"items": []map[string]any{{"id": f.id, "name": f.name}}})
	case "document_get":
		typ := f.typ
		if typ == "" {
			typ = "task"
		}
		resp := verb.DocumentGetResponse{RawBody: f.body}
		resp.Document.ID = f.id
		resp.Document.Name = f.name
		resp.Document.Type = typ
		resp.Document.Tags = f.tags
		return json.Marshal(resp)
	case "document_upsert":
		var m map[string]any
		_ = json.Unmarshal(req, &m)
		f.upserts = append(f.upserts, m)
		return json.Marshal(map[string]any{"document": map[string]any{"id": f.id, "latest_version": 1}, "version": map[string]any{"version": 1}})
	}
	return json.RawMessage(`{}`), nil
}

// TestPublishProjectTask_ByID covers AC1/AC2: publishing a project task by id
// copies it into scope:library under the publisher namespace, provenance-stamped,
// as a faithful copy of the body.
func TestPublishProjectTask_ByID(t *testing.T) {
	f := &fakeTaskPublishDispatch{
		id:   "tsk_abc",
		name: "codegraph",
		body: "# codegraph\n\n## Action\nGenerate the graph.\n",
		tags: []string{"area:codeindex"},
	}
	var buf bytes.Buffer
	if err := publishProjectTask(context.Background(), &buf, f.dispatch, "tsk_abc", "proj_pub", false); err != nil {
		t.Fatal(err)
	}
	if len(f.upserts) != 1 {
		t.Fatalf("want 1 library upsert, got %d", len(f.upserts))
	}
	up := f.upserts[0]
	if up["type"] != "task" || up["scope"] != "library" || up["project_id"] != "proj_pub" || up["name"] != "codegraph" {
		t.Fatalf("library payload wrong: %v", up)
	}
	body, _ := up["body"].(string)
	if !strings.Contains(body, "satellites-library:begin") {
		t.Errorf("library body must carry the provenance stamp, got %q", body)
	}
	if !strings.Contains(body, "Generate the graph.") {
		t.Errorf("library body must faithfully copy the task body, got %q", body)
	}
}

// TestPublishProjectTask_ByName resolves the task by its exact title.
func TestPublishProjectTask_ByName(t *testing.T) {
	f := &fakeTaskPublishDispatch{id: "tsk_xyz", name: "codegraph", body: "# codegraph\nbody\n"}
	var buf bytes.Buffer
	if err := publishProjectTask(context.Background(), &buf, f.dispatch, "codegraph", "proj_pub", false); err != nil {
		t.Fatal(err)
	}
	if len(f.upserts) != 1 || f.upserts[0]["name"] != "codegraph" {
		t.Fatalf("want a library upsert named codegraph, got %v", f.upserts)
	}
}

// TestPublishProjectTask_DropsForkedFrom: publishing an original strips any
// consumer forked-from stamp so the library copy is not marked consumed.
func TestPublishProjectTask_DropsForkedFrom(t *testing.T) {
	f := &fakeTaskPublishDispatch{
		id:   "tsk_abc",
		name: "codegraph",
		body: "# codegraph\nbody\n",
		tags: []string{"area:codeindex", "forked-from:proj_other/codegraph"},
	}
	var buf bytes.Buffer
	if err := publishProjectTask(context.Background(), &buf, f.dispatch, "tsk_abc", "proj_pub", false); err != nil {
		t.Fatal(err)
	}
	tags, _ := f.upserts[0]["tags"].([]any)
	for _, tg := range tags {
		if s, _ := tg.(string); strings.HasPrefix(s, "forked-from:") {
			t.Errorf("forked-from stamp must be dropped on publish, got tags %v", tags)
		}
	}
}

// TestPublishProjectTask_DryRunDispatchesNothing covers the headless dry-run.
func TestPublishProjectTask_DryRunDispatchesNothing(t *testing.T) {
	f := &fakeTaskPublishDispatch{id: "tsk_abc", name: "codegraph", body: "# codegraph\nbody\n"}
	var buf bytes.Buffer
	if err := publishProjectTask(context.Background(), &buf, f.dispatch, "tsk_abc", "proj_pub", true); err != nil {
		t.Fatal(err)
	}
	if len(f.upserts) != 0 {
		t.Fatalf("dry-run must dispatch nothing, got %d upserts", len(f.upserts))
	}
	if !strings.Contains(buf.String(), "would publish proj_pub/codegraph") {
		t.Errorf("want a dry-run line, got %q", buf.String())
	}
}

// TestPublishProjectTask_RejectsNonTask: a non-task id is refused.
func TestPublishProjectTask_RejectsNonTask(t *testing.T) {
	f := &fakeTaskPublishDispatch{id: "tsk_abc", name: "x", typ: "story", body: "b"}
	var buf bytes.Buffer
	err := publishProjectTask(context.Background(), &buf, f.dispatch, "tsk_abc", "proj_pub", false)
	if err == nil || !strings.Contains(err.Error(), "not a task") {
		t.Fatalf("want a non-task rejection, got %v", err)
	}
}
