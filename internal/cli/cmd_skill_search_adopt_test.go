package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestSkillDescription_CoherentKey pins the fix for the search scope-coherence
// bug: skillDescription must emit a document_get key that satisfies the
// scope-coherence rules (library carries project_id and no workspace_id;
// project carries both; system/workspace carry only what their scope allows),
// so a list item with an under-populated workspace_id never trips the check.
func TestSkillDescription_CoherentKey(t *testing.T) {
	cases := []struct {
		scope, ws, pj  string
		wantWS, wantPJ bool
	}{
		{"library", "", "proj_pub", false, true},
		{"system", "", "", false, false},
		{"workspace", "wksp_x", "", true, false},
		{"project", "wksp_x", "proj_y", true, true},
	}
	for _, c := range cases {
		var gotKey struct {
			Scope       string `json:"scope"`
			WorkspaceID string `json:"workspace_id"`
			ProjectID   string `json:"project_id"`
		}
		dispatch := func(_ context.Context, _ string, req json.RawMessage) (json.RawMessage, error) {
			if err := json.Unmarshal(req, &gotKey); err != nil {
				t.Fatalf("decode get req: %v", err)
			}
			return json.RawMessage(`{"raw_body":"---\nname: s\ndescription: d\n---\n"}`), nil
		}
		if _, err := skillDescription(context.Background(), dispatch, c.scope, c.ws, c.pj, "s"); err != nil {
			t.Fatalf("scope %s: %v", c.scope, err)
		}
		if gotKey.Scope != c.scope {
			t.Fatalf("scope %s: got scope %q", c.scope, gotKey.Scope)
		}
		if (gotKey.WorkspaceID != "") != c.wantWS {
			t.Fatalf("scope %s: workspace_id presence = %v, want %v", c.scope, gotKey.WorkspaceID != "", c.wantWS)
		}
		if (gotKey.ProjectID != "") != c.wantPJ {
			t.Fatalf("scope %s: project_id presence = %v, want %v", c.scope, gotKey.ProjectID != "", c.wantPJ)
		}
	}
}

func TestInjectForkedFrom(t *testing.T) {
	t.Run("inside existing frontmatter", func(t *testing.T) {
		body := "---\nname: s\ndescription: d\n---\n\n# S\n"
		got := injectForkedFrom(body, "proj_a/s@3")
		want := "---\nname: s\ndescription: d\nforked-from: proj_a/s@3\n---\n\n# S\n"
		if got != want {
			t.Fatalf("placement wrong:\n got: %q\nwant: %q", got, want)
		}
	})

	t.Run("no frontmatter wraps a fresh block", func(t *testing.T) {
		got := injectForkedFrom("# S\n", "proj_a/s@1")
		if !strings.HasPrefix(got, "---\nforked-from: proj_a/s@1\n---\n") {
			t.Fatalf("missing wrapped block: %q", got)
		}
		if !strings.Contains(got, "# S") {
			t.Fatalf("body lost: %q", got)
		}
	})

	t.Run("re-adopt replaces the stamp", func(t *testing.T) {
		body := "---\nname: s\nforked-from: proj_a/s@1\n---\n# S\n"
		got := injectForkedFrom(body, "proj_b/s@7")
		if strings.Contains(got, "proj_a/s@1") {
			t.Fatalf("stale stamp survived: %q", got)
		}
		if n := strings.Count(got, "forked-from:"); n != 1 {
			t.Fatalf("stamp count=%d want 1: %q", n, got)
		}
	})
}

// TestReviewContent_FrontmatterExempt pins the AC4 enabler: substrate ids in
// the frontmatter (a forked-from stamp's publisher project id) pass the
// strict review, while the same id in authored prose still blocks.
func TestReviewContent_FrontmatterExempt(t *testing.T) {
	fmOnly := "---\nname: s\nforked-from: proj_60641e91/s@2\n---\n\n# S\n\nClean prose.\n"
	if findings := reviewContent(fmOnly); len(findings) != 0 {
		t.Fatalf("frontmatter id flagged: %+v", findings)
	}
	prose := "---\nname: s\n---\n\nTracked in sty_deadbeef.\n"
	if findings := reviewContent(prose); len(findings) != 1 {
		t.Fatalf("prose id not flagged exactly once: %+v", findings)
	}
	// A markdown horizontal rule mid-document does not open a fake
	// frontmatter zone that would swallow later prose ids.
	rule := "# S\n\n---\n\nTracked in sty_deadbeef.\n"
	if findings := reviewContent(rule); len(findings) != 1 {
		t.Fatalf("id after horizontal rule not flagged: %+v", findings)
	}
}
