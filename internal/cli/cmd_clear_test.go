package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// clearFakeStore models the project's substrate for the clear tests: a
// project-scoped document_list returns the rows for the named scope/type, and
// document_delete records the row it tombstoned. A request that names no
// project_id, or the system scope, would surface foreign/shared rows — the
// fake serves those so a test can prove clear never asks for them.
type clearFakeStore struct {
	projectSkills []nounListItem // type:skill, scope:project
	projectDocs   []nounListItem // every project row (incl. principles + a story)
	systemRows    []nounListItem // scope:system — must never be requested
	deleted       []string       // names passed to document_delete
	sawSystem     bool
	sawNoProject  bool
}

func (s *clearFakeStore) dispatch(_ context.Context, name string, raw json.RawMessage) (json.RawMessage, error) {
	switch name {
	case "document_list":
		var req docListRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
		if req.Scope == "system" {
			s.sawSystem = true
			return json.Marshal(docListView{Items: s.systemRows})
		}
		if req.Scope == "project" && req.ProjectID == "" {
			s.sawNoProject = true
		}
		if req.Type == "skill" {
			return json.Marshal(docListView{Items: s.projectSkills})
		}
		return json.Marshal(docListView{Items: s.projectDocs})
	case "document_delete":
		var req map[string]string
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
		s.deleted = append(s.deleted, req["name"])
		return json.Marshal(map[string]any{"version": map[string]any{"status": "deleted", "version": 2}})
	default:
		return nil, fmt.Errorf("unexpected verb %q", name)
	}
}

func newClearFakeStore() *clearFakeStore {
	return &clearFakeStore{
		systemRows: []nounListItem{
			{Name: "agent-goals", Scope: "system", Type: "document", Tags: []string{"principles:always"}},
			{Name: "satellites-story-summary", Scope: "system", Type: "skill", Tags: []string{"kind:reviewer"}},
		},
		projectSkills: []nounListItem{
			{Name: "vire-done-review", Scope: "project", Type: "skill", Tags: []string{"kind:reviewer"}},
			{Name: "vire-release-workflow", Scope: "project", Type: "skill", Tags: []string{"kind:workflow"}},
		},
		projectDocs: []nounListItem{
			{Name: "constitution", Scope: "project", Type: "document", Tags: []string{"principles:always"}},
			{Name: "broken-windows", Scope: "project", Type: "document", Tags: []string{"principles:always"}},
			// A story shares the project scope — it must never be cleared.
			{Name: "Some story", Scope: "project", Type: "story", Tags: []string{"epic:x"}},
		},
	}
}

// TestClearTargets_KindSelectors pins what each --kind enumerates, that the
// system scope is never requested, and that a project-scope story is dropped.
func TestClearTargets_KindSelectors(t *testing.T) {
	cases := []struct {
		kind string
		want []string
	}{
		{"principle", []string{"broken-windows", "constitution"}},
		{"workflow", []string{"vire-release-workflow"}},
		{"skill", []string{"vire-done-review", "vire-release-workflow"}},
		{"all", []string{"broken-windows", "constitution", "vire-done-review", "vire-release-workflow"}},
	}
	for _, c := range cases {
		t.Run(c.kind, func(t *testing.T) {
			s := newClearFakeStore()
			got, err := clearTargets(context.Background(), s.dispatch, c.kind, "proj_A", "wksp_A")
			if err != nil {
				t.Fatalf("clearTargets: %v", err)
			}
			var names []string
			for _, it := range got {
				if it.Type == "story" {
					t.Fatalf("a story entered the kill-list: %q", it.Name)
				}
				names = append(names, it.Name)
			}
			if strings.Join(names, ",") != strings.Join(c.want, ",") {
				t.Errorf("kind %s targets = %v, want %v", c.kind, names, c.want)
			}
			if s.sawSystem {
				t.Errorf("kind %s requested the system scope — clear must stay project-scoped", c.kind)
			}
			if s.sawNoProject {
				t.Errorf("kind %s issued a project list with no project_id (would leak foreign rows)", c.kind)
			}
		})
	}
}

// TestRunClearVia_DryRunWritesNothing proves --dryrun enumerates but issues zero
// document_delete calls, and never prompts (even non-interactive).
func TestRunClearVia_DryRunWritesNothing(t *testing.T) {
	s := newClearFakeStore()
	var out bytes.Buffer
	// dryrun=true, force=false, interactive=false → must still not error or delete.
	if err := runClearVia(context.Background(), &out, strings.NewReader(""), s.dispatch, "all", "proj_A", "wksp_A", true, false, false); err != nil {
		t.Fatalf("runClearVia: %v", err)
	}
	if len(s.deleted) != 0 {
		t.Errorf("dry-run deleted %v, want none", s.deleted)
	}
	if !strings.Contains(out.String(), "dry-run") {
		t.Errorf("dry-run output missing the dry-run notice:\n%s", out.String())
	}
}

// TestRunClearVia_ForceDeletesEachRow proves --force issues exactly one
// document_delete per enumerated row, in any context (here non-interactive).
func TestRunClearVia_ForceDeletesEachRow(t *testing.T) {
	s := newClearFakeStore()
	var out bytes.Buffer
	// force=true, interactive=false → deletes without a prompt.
	if err := runClearVia(context.Background(), &out, strings.NewReader(""), s.dispatch, "all", "proj_A", "wksp_A", false, true, false); err != nil {
		t.Fatalf("runClearVia: %v", err)
	}
	want := []string{"broken-windows", "constitution", "vire-done-review", "vire-release-workflow"}
	if strings.Join(s.deleted, ",") != strings.Join(want, ",") {
		t.Errorf("deleted %v, want one delete per row %v", s.deleted, want)
	}
}

// TestRunClearVia_NonInteractiveWithoutForceRefuses proves a non-TTY run with no
// --force ERRORS and deletes nothing — neither silent abort nor silent delete.
func TestRunClearVia_NonInteractiveWithoutForceRefuses(t *testing.T) {
	s := newClearFakeStore()
	var out bytes.Buffer
	err := runClearVia(context.Background(), &out, strings.NewReader(""), s.dispatch, "all", "proj_A", "wksp_A", false, false, false)
	if err == nil {
		t.Fatal("non-interactive run without --force must error")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error should instruct --force, got: %v", err)
	}
	if len(s.deleted) != 0 {
		t.Errorf("refused run still deleted %v", s.deleted)
	}
}

// TestRunClearVia_InteractiveConfirm proves the interactive path: "y" deletes,
// "n" aborts with nothing deleted.
func TestRunClearVia_InteractiveConfirm(t *testing.T) {
	yes := newClearFakeStore()
	var out bytes.Buffer
	if err := runClearVia(context.Background(), &out, strings.NewReader("y\n"), yes.dispatch, "all", "proj_A", "wksp_A", false, false, true); err != nil {
		t.Fatalf("interactive yes: %v", err)
	}
	if len(yes.deleted) != 4 {
		t.Errorf("interactive yes deleted %v, want all 4", yes.deleted)
	}

	no := newClearFakeStore()
	out.Reset()
	if err := runClearVia(context.Background(), &out, strings.NewReader("n\n"), no.dispatch, "all", "proj_A", "wksp_A", false, false, true); err != nil {
		t.Fatalf("interactive no: %v", err)
	}
	if len(no.deleted) != 0 {
		t.Errorf("interactive decline still deleted %v", no.deleted)
	}
	if !strings.Contains(out.String(), "aborted") {
		t.Errorf("missing abort notice:\n%s", out.String())
	}
}

// TestRunClearVia_InteractiveEOFRefuses proves the harness case: a terminal is
// detected (interactive=true) but no answer arrives (EOF) → error demanding
// --force, nothing deleted. This is the agent-via-pty path the silent-abort
// bug lived in.
func TestRunClearVia_InteractiveEOFRefuses(t *testing.T) {
	s := newClearFakeStore()
	var out bytes.Buffer
	err := runClearVia(context.Background(), &out, strings.NewReader(""), s.dispatch, "all", "proj_A", "wksp_A", false, false, true)
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("EOF with no answer must error demanding --force, got: %v", err)
	}
	if len(s.deleted) != 0 {
		t.Errorf("EOF refusal still deleted %v", s.deleted)
	}
}
