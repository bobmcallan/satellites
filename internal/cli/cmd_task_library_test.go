package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// fakeLibDispatch answers the library-list / project-list / get / upsert reads
// copyLibraryTask + listAllLibraryTaskRefs make, so the browse-and-copy logic
// can be asserted without a server. Library items carry a publisher (project_id),
// unlike the sync test's fake.
type fakeLibDispatch struct {
	lib       []libTaskRef      // published library tasks (name + publisher [+ headline])
	bodies    map[string]string // "publisher/name" → body
	projTasks []projTask        // existing tasks in the consuming project
	upserts   []map[string]any  // every document_upsert payload, in order
}

func (f *fakeLibDispatch) dispatch(_ context.Context, name string, req json.RawMessage) (json.RawMessage, error) {
	var m map[string]any
	_ = json.Unmarshal(req, &m)
	switch name {
	case "document_list":
		if scope, _ := m["scope"].(string); scope == "library" {
			pub, _ := m["project_id"].(string)
			items := []map[string]any{}
			for _, r := range f.lib {
				if pub != "" && r.Publisher != pub {
					continue
				}
				items = append(items, map[string]any{"name": r.Name, "project_id": r.Publisher, "headline": r.Headline})
			}
			return json.Marshal(map[string]any{"items": items})
		}
		items := []map[string]any{}
		for _, pt := range f.projTasks {
			items = append(items, map[string]any{"id": pt.ID, "name": pt.Name, "tags": pt.Tags})
		}
		return json.Marshal(map[string]any{"items": items})
	case "document_get":
		n, _ := m["name"].(string)
		pub, _ := m["project_id"].(string)
		return json.Marshal(map[string]any{
			"raw_body": f.bodies[pub+"/"+n],
			"document": map[string]any{"latest_version": 1},
		})
	case "document_upsert":
		f.upserts = append(f.upserts, m)
		return json.Marshal(map[string]any{"document": map[string]any{"id": "tsk_new", "latest_version": 1}})
	}
	return json.RawMessage(`{}`), nil
}

func TestCopyLibraryTask_CreatesFromLibrary(t *testing.T) {
	f := &fakeLibDispatch{
		lib:    []libTaskRef{{Name: "Codegraph", Publisher: "proj_pub"}},
		bodies: map[string]string{"proj_pub/Codegraph": "# Codegraph task body"},
	}
	var buf bytes.Buffer
	if err := copyLibraryTask(context.Background(), &buf, f.dispatch, "proj_consumer", "Codegraph", ""); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if len(f.upserts) != 1 {
		t.Fatalf("want 1 upsert, got %d", len(f.upserts))
	}
	up := f.upserts[0]
	if up["type"] != "task" || up["scope"] != "project" || up["project_id"] != "proj_consumer" || up["name"] != "Codegraph" {
		t.Fatalf("create payload wrong: %v", up)
	}
	if _, hasID := up["id"]; hasID {
		t.Error("a fresh copy must NOT carry an id (mints a new tsk_)")
	}
	tags, _ := up["tags"].([]any)
	if len(tags) != 1 || tags[0] != "forked-from:proj_pub/Codegraph" {
		t.Errorf("want forked-from provenance, got %v", tags)
	}
	if !strings.Contains(buf.String(), "copied") {
		t.Errorf("expected a 'copied' verdict, got: %q", buf.String())
	}
}

func TestCopyLibraryTask_AmbiguousNeedsFrom(t *testing.T) {
	f := &fakeLibDispatch{
		lib: []libTaskRef{
			{Name: "Codegraph", Publisher: "proj_a"},
			{Name: "Codegraph", Publisher: "proj_b"},
		},
		bodies: map[string]string{"proj_a/Codegraph": "a", "proj_b/Codegraph": "b"},
	}
	var buf bytes.Buffer
	err := copyLibraryTask(context.Background(), &buf, f.dispatch, "proj_consumer", "Codegraph", "")
	if err == nil || !strings.Contains(err.Error(), "--from") {
		t.Fatalf("ambiguous name should demand --from, got err=%v", err)
	}
	if len(f.upserts) != 0 {
		t.Errorf("ambiguous copy must not write anything")
	}
	// --from disambiguates → one create.
	if err := copyLibraryTask(context.Background(), &buf, f.dispatch, "proj_consumer", "Codegraph", "proj_b"); err != nil {
		t.Fatalf("copy --from: %v", err)
	}
	if len(f.upserts) != 1 || f.upserts[0]["tags"].([]any)[0] != "forked-from:proj_b/Codegraph" {
		t.Errorf("--from should copy proj_b's task, got %v", f.upserts)
	}
}

func TestCopyLibraryTask_LocalPrecedence(t *testing.T) {
	f := &fakeLibDispatch{
		lib:       []libTaskRef{{Name: "Codegraph", Publisher: "proj_pub"}},
		bodies:    map[string]string{"proj_pub/Codegraph": "lib"},
		projTasks: []projTask{{ID: "tsk_local", Name: "Codegraph", Tags: nil, ForkedSrc: ""}},
	}
	var buf bytes.Buffer
	err := copyLibraryTask(context.Background(), &buf, f.dispatch, "proj_consumer", "Codegraph", "")
	if err == nil || !strings.Contains(err.Error(), "local") {
		t.Fatalf("a local same-name task should block the copy, got err=%v", err)
	}
	if len(f.upserts) != 0 {
		t.Errorf("local precedence must not overwrite: %v", f.upserts)
	}
}

func TestCopyLibraryTask_ReconcilesExistingFork(t *testing.T) {
	f := &fakeLibDispatch{
		lib:       []libTaskRef{{Name: "Codegraph", Publisher: "proj_pub"}},
		bodies:    map[string]string{"proj_pub/Codegraph": "newer body"},
		projTasks: []projTask{{ID: "tsk_forked", Name: "Codegraph", Tags: []string{"forked-from:proj_pub/Codegraph"}, ForkedSrc: "proj_pub/Codegraph"}},
	}
	var buf bytes.Buffer
	if err := copyLibraryTask(context.Background(), &buf, f.dispatch, "proj_consumer", "Codegraph", ""); err != nil {
		t.Fatalf("re-copy: %v", err)
	}
	if len(f.upserts) != 1 || f.upserts[0]["id"] != "tsk_forked" {
		t.Fatalf("re-copy should reconcile the existing fork by id, got %v", f.upserts)
	}
	if !strings.Contains(buf.String(), "updated") {
		t.Errorf("expected an 'updated' verdict, got: %q", buf.String())
	}
}

func TestListAllLibraryTaskRefs_SortedWithPublisher(t *testing.T) {
	f := &fakeLibDispatch{lib: []libTaskRef{
		{Name: "Zeta", Publisher: "proj_a"},
		{Name: "Alpha", Publisher: "proj_b", Headline: "first"},
	}}
	refs, err := listAllLibraryTaskRefs(context.Background(), f.dispatch)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 || refs[0].Name != "Alpha" || refs[0].Publisher != "proj_b" || refs[0].Headline != "first" {
		t.Fatalf("refs not sorted/populated: %v", refs)
	}
}
