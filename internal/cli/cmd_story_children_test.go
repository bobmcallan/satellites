package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/verb"
)

// fakeChildrenDispatch returns an anchor (via document_get) in anchorProject,
// and a document_list result that ONLY yields the children when the list is
// scoped to listProject. This models the substrate's project confinement: a
// query scoped to the wrong project comes back empty. It records the project_id
// the command actually asked document_list for.
func fakeChildrenDispatch(t *testing.T, anchor document.Document, listProject string, children []childRow, gotListProject *string) verbDispatch {
	t.Helper()
	return func(_ context.Context, name string, req json.RawMessage) (json.RawMessage, error) {
		switch name {
		case "document_get":
			return json.Marshal(verb.DocumentGetResponse{Document: anchor})
		case "document_list":
			var lr docListRequest
			if err := json.Unmarshal(req, &lr); err != nil {
				t.Fatalf("decode list req: %v", err)
			}
			if gotListProject != nil {
				*gotListProject = lr.ProjectID
			}
			var items []childRow
			if lr.ProjectID == listProject {
				items = children
			}
			return json.Marshal(struct {
				Items []childRow `json:"items"`
			}{Items: items})
		default:
			t.Fatalf("unexpected verb %q", name)
			return nil, nil
		}
	}
}

// The anchor's children must resolve from the ANCHOR's project, not the caller's
// config project — so the same call returns the same children regardless of cwd
// (sty_e383c48b). The fake only yields children for project "A"; the command
// must derive "A" from the anchor to find them.
func TestStoryChildren_ResolvesAnchorProjectNotConfig(t *testing.T) {
	anchor := document.Document{ID: "sty_anchor", Type: "story", ProjectID: "A"}
	kids := []childRow{
		{ID: "sty_c1", Status: "done", Parent: "sty_anchor"},
		{ID: "sty_c2", Status: "done", Parent: "sty_anchor"},
	}
	var asked string
	d := fakeChildrenDispatch(t, anchor, "A", kids, &asked)

	var out bytes.Buffer
	if err := runStoryChildren(context.Background(), &out, d, "sty_anchor"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if asked != "A" {
		t.Fatalf("document_list scoped to %q, want anchor project %q", asked, "A")
	}
	if !strings.Contains(out.String(), "2 child(ren), 0 non-terminal") {
		t.Fatalf("expected 2 terminal children, got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "the anchor may close") {
		t.Fatalf("expected may-close, got:\n%s", out.String())
	}
}

// Regression for the false "may close": a non-terminal child in the anchor's
// project must surface as non-terminal (exit error), never a vacuous may-close.
func TestStoryChildren_NonTerminalChildBlocksClose(t *testing.T) {
	anchor := document.Document{ID: "sty_anchor", Type: "story", ProjectID: "A"}
	kids := []childRow{
		{ID: "sty_c1", Status: "done", Parent: "sty_anchor"},
		{ID: "sty_c2", Status: "in_progress", Parent: "sty_anchor"},
	}
	d := fakeChildrenDispatch(t, anchor, "A", kids, nil)

	var out bytes.Buffer
	err := runStoryChildren(context.Background(), &out, d, "sty_anchor")
	if err == nil {
		t.Fatalf("expected non-terminal error, got nil; output:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "non-terminal") {
		t.Fatalf("expected non-terminal error, got %v", err)
	}
	if strings.Contains(out.String(), "the anchor may close") {
		t.Fatalf("must NOT report may-close with a non-terminal child:\n%s", out.String())
	}
}

// A wrong-type anchor id must fail loudly, never silently report "0 children".
func TestStoryChildren_WrongTypeAnchorFailsLoud(t *testing.T) {
	anchor := document.Document{ID: "doc_x", Type: "document", ProjectID: "A"}
	d := fakeChildrenDispatch(t, anchor, "A", nil, nil)

	var out bytes.Buffer
	err := runStoryChildren(context.Background(), &out, d, "doc_x")
	if err == nil || !strings.Contains(err.Error(), "not a story") {
		t.Fatalf("expected not-a-story error, got %v", err)
	}
	if strings.Contains(out.String(), "may close") {
		t.Fatalf("must not report may-close for a non-story anchor:\n%s", out.String())
	}
}

// An anchor with no project must fail loudly rather than query an empty scope.
func TestStoryChildren_NoProjectFailsLoud(t *testing.T) {
	anchor := document.Document{ID: "sty_anchor", Type: "story", ProjectID: ""}
	d := fakeChildrenDispatch(t, anchor, "A", nil, nil)

	var out bytes.Buffer
	err := runStoryChildren(context.Background(), &out, d, "sty_anchor")
	if err == nil || !strings.Contains(err.Error(), "no project") {
		t.Fatalf("expected no-project error, got %v", err)
	}
}
