package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolvePublishScope pins AC2: publish routes by the declared level (or
// scope fallback) — global→library, project→project, system→refused, empty→library.
func TestResolvePublishScope(t *testing.T) {
	cases := []struct {
		level, scope string
		want         string
		wantErr      bool
	}{
		{"global", "", "library", false},
		{"project", "", "project", false},
		{"system", "", "", true},
		{"", "library", "library", false}, // scope fallback
		{"", "project", "project", false},
		{"", "system", "", true},
		{"", "", "library", false},              // back-compat default
		{"global", "project", "library", false}, // level wins over scope
		{"weird", "", "", true},
	}
	for _, c := range cases {
		got, err := resolvePublishScope(c.level, c.scope)
		if c.wantErr {
			if err == nil {
				t.Errorf("resolvePublishScope(%q,%q) = %q, want error", c.level, c.scope, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("resolvePublishScope(%q,%q) = (%q,%v), want (%q,nil)", c.level, c.scope, got, err, c.want)
		}
	}
}

// writeToml writes a satellites.toml under a temp dir and returns its path.
func writeToml(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	sat := filepath.Join(dir, ".satellites")
	if err := os.MkdirAll(sat, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(sat, "satellites.toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// libraryDispatch fakes the substrate for a single publisher's library list+get.
func libraryDispatch(t *testing.T, publisher string) verbDispatch {
	t.Helper()
	return func(_ context.Context, name string, req json.RawMessage) (json.RawMessage, error) {
		switch name {
		case "document_list":
			var lr struct {
				Scope     string `json:"scope"`
				ProjectID string `json:"project_id"`
			}
			_ = json.Unmarshal(req, &lr)
			if lr.Scope != "library" || lr.ProjectID != publisher {
				return json.RawMessage(`{"items":[]}`), nil
			}
			return json.RawMessage(`{"items":[{"id":"doc_g","name":"the-gate","scope":"library","project_id":"` + publisher + `","latest_version":2}]}`), nil
		case "document_get":
			return json.RawMessage(`{"raw_body":"---\nname: the-gate\nkind: gate\n---\nbody","document":{"id":"doc_g","latest_version":2}}`), nil
		}
		return nil, fmt.Errorf("unexpected verb %q", name)
	}
}

// TestListGlobalSkills_OptInPublisher pins AC3: sync pulls a publisher's global
// (library) artifacts when the repo opts into the publisher.
func TestListGlobalSkills_OptInPublisher(t *testing.T) {
	cfg := writeToml(t, "server_url=\"x\"\nglobal_publishers = [\"proj_682cfeed\"]\n")
	var out bytes.Buffer
	got, err := listGlobalSkills(context.Background(), &out, libraryDispatch(t, "proj_682cfeed"), cfg)
	if err != nil {
		t.Fatalf("listGlobalSkills: %v", err)
	}
	if len(got) != 1 || got[0].Name != "the-gate" || got[0].Publisher != "proj_682cfeed" {
		t.Fatalf("got %+v, want one the-gate stamped proj_682cfeed", got)
	}
	if strings.Contains(out.String(), "deprecated") {
		t.Errorf("a global_publishers config must not emit the deprecation note: %q", out.String())
	}
}

// TestListGlobalSkills_DerivesFromLibraryPins pins AC4 back-compat: with no
// global_publishers but a remaining library_pins, the publisher set is derived
// and a deprecation note is printed.
func TestListGlobalSkills_DerivesFromLibraryPins(t *testing.T) {
	cfg := writeToml(t, "server_url=\"x\"\nlibrary_pins = [\"proj_682cfeed/the-gate\", \"proj_682cfeed/other\"]\n")
	var out bytes.Buffer
	got, err := listGlobalSkills(context.Background(), &out, libraryDispatch(t, "proj_682cfeed"), cfg)
	if err != nil {
		t.Fatalf("listGlobalSkills: %v", err)
	}
	if len(got) == 0 || got[0].Publisher != "proj_682cfeed" {
		t.Fatalf("derived consumption must still pull from proj_682cfeed, got %+v", got)
	}
	if !strings.Contains(out.String(), "deprecated") {
		t.Errorf("a library_pins-only config must emit the deprecation note, got %q", out.String())
	}
}
