package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeExecConfig(t *testing.T, pid string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "satellites.toml")
	if err := os.WriteFile(p, []byte("project_id = \""+pid+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestConfineDefaultsWhenOmitted: a project-scoped verb run from a repo, with no
// project_id, defaults to the config project (the cross-project-leak guard).
func TestConfineDefaultsWhenOmitted(t *testing.T) {
	cfg := writeExecConfig(t, "proj_repo")
	out := confineToConfigProject("document_list", json.RawMessage(`{"type":"story"}`), cfg)
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if m["project_id"] != "proj_repo" {
		t.Fatalf("project_id = %v, want proj_repo (default not applied)", m["project_id"])
	}
	if m["type"] != "story" {
		t.Fatalf("existing field lost: %v", m)
	}
}

// TestConfineHonoursExplicit: an explicit project_id is never overridden (admin
// can still target another project deliberately).
func TestConfineHonoursExplicit(t *testing.T) {
	cfg := writeExecConfig(t, "proj_repo")
	in := json.RawMessage(`{"type":"story","project_id":"proj_other"}`)
	out := confineToConfigProject("document_list", in, cfg)
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if m["project_id"] != "proj_other" {
		t.Fatalf("explicit project_id overridden to %v", m["project_id"])
	}
}

// TestConfineNonProjectScopedUnchanged: a verb that isn't project-scoped is left
// exactly as-is.
func TestConfineNonProjectScopedUnchanged(t *testing.T) {
	cfg := writeExecConfig(t, "proj_repo")
	in := json.RawMessage(`{"name":"x"}`)
	out := confineToConfigProject("workspace_create", in, cfg)
	if string(out) != string(in) {
		t.Fatalf("non-project-scoped verb mutated: %s", out)
	}
}

// TestConfineNoConfigUnchanged: out of a repo (no resolvable config project),
// nothing changes.
func TestConfineNoConfigUnchanged(t *testing.T) {
	in := json.RawMessage(`{"type":"story"}`)
	out := confineToConfigProject("document_list", in, filepath.Join(t.TempDir(), "nope.toml"))
	if string(out) != string(in) {
		t.Fatalf("no-config context mutated the request: %s", out)
	}
}

// TestConfineEmptyReqInjects: an unscoped verb with no args at all still gets the
// project default (the bare `exec document_list` leak).
func TestConfineEmptyReqInjects(t *testing.T) {
	cfg := writeExecConfig(t, "proj_repo")
	out := confineToConfigProject("document_list", nil, cfg)
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("empty req should yield a JSON object: %v", err)
	}
	if m["project_id"] != "proj_repo" {
		t.Fatalf("empty req should inject project_id, got %v", m)
	}
}
