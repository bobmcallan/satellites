package cli

import (
	"strings"
	"testing"
)

// TestParseTaskInputs covers the `## Inputs` declaration parser (B1 AC1): a
// tags-only block, an ids-only block, both together, the no-section case, and
// the two malformed cases (bad yaml, empty declaration).
func TestParseTaskInputs(t *testing.T) {
	t.Run("tags only", func(t *testing.T) {
		body := "# T\n\n## Inputs\n\n```yaml\ntags: [phase:discovery, type:document]\n```\n\n## ACTION\nwork\n"
		spec, ok, err := parseTaskInputs(body)
		if err != nil || !ok {
			t.Fatalf("want ok, got ok=%v err=%v", ok, err)
		}
		if len(spec.Tags) != 2 || spec.Tags[0] != "phase:discovery" || spec.Tags[1] != "type:document" {
			t.Fatalf("tags = %v", spec.Tags)
		}
		if len(spec.IDs) != 0 {
			t.Fatalf("ids = %v, want none", spec.IDs)
		}
	})

	t.Run("ids only", func(t *testing.T) {
		body := "## Inputs\n```yaml\nids:\n  - doc_a\n  - doc_b\n```\n"
		spec, ok, err := parseTaskInputs(body)
		if err != nil || !ok {
			t.Fatalf("want ok, got ok=%v err=%v", ok, err)
		}
		if len(spec.IDs) != 2 || spec.IDs[0] != "doc_a" || spec.IDs[1] != "doc_b" {
			t.Fatalf("ids = %v", spec.IDs)
		}
	})

	t.Run("both, bare fence", func(t *testing.T) {
		body := "## Inputs\n```\ntags: [phase:build]\nids: [doc_x]\n```\n"
		spec, ok, err := parseTaskInputs(body)
		if err != nil || !ok {
			t.Fatalf("want ok, got ok=%v err=%v", ok, err)
		}
		if len(spec.Tags) != 1 || len(spec.IDs) != 1 {
			t.Fatalf("spec = %+v", spec)
		}
	})

	t.Run("no inputs section", func(t *testing.T) {
		body := "# T\n\n## ACTION\nwork\n\n## OUTPUT\ndocs/x.md\n"
		_, ok, err := parseTaskInputs(body)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if ok {
			t.Fatal("want ok=false when no ## Inputs section")
		}
	})

	t.Run("section ends at next heading (no fence)", func(t *testing.T) {
		body := "## Inputs\n\nsome prose, no yaml block\n\n## ACTION\nwork\n"
		_, ok, err := parseTaskInputs(body)
		if err != nil || ok {
			t.Fatalf("a section with no fenced block resolves to no inputs: ok=%v err=%v", ok, err)
		}
	})

	t.Run("malformed yaml", func(t *testing.T) {
		body := "## Inputs\n```yaml\ntags: [unterminated\n```\n"
		_, _, err := parseTaskInputs(body)
		if err == nil {
			t.Fatal("want error on malformed yaml")
		}
	})

	t.Run("empty declaration", func(t *testing.T) {
		body := "## Inputs\n```yaml\ntags: []\nids: []\n```\n"
		_, _, err := parseTaskInputs(body)
		if err == nil || !strings.Contains(err.Error(), "neither tags nor ids") {
			t.Fatalf("want 'neither tags nor ids' error, got %v", err)
		}
	})
}

// TestSelectInputsProjectPin pins B1 AC3: resolution is project-scoped — a
// candidate from another project is dropped, and the result is deduped by id in
// discovery order.
func TestSelectInputsProjectPin(t *testing.T) {
	const pinned = "proj_self"
	cands := []inputCandidate{
		{input: resolvedInput{ID: "doc_1", Name: "a"}, projectID: pinned},
		{input: resolvedInput{ID: "doc_foreign", Name: "leak"}, projectID: "proj_other"}, // dropped
		{input: resolvedInput{ID: "doc_2", Name: "b"}, projectID: pinned},
		{input: resolvedInput{ID: "doc_1", Name: "a-dup"}, projectID: pinned}, // dedup
	}
	got := selectInputs(pinned, cands)
	if len(got) != 2 {
		t.Fatalf("want 2 resolved, got %d: %+v", len(got), got)
	}
	if got[0].ID != "doc_1" || got[1].ID != "doc_2" {
		t.Fatalf("order/identity wrong: %+v", got)
	}
	for _, r := range got {
		if r.ID == "doc_foreign" {
			t.Fatal("a foreign-project document leaked into the resolved set")
		}
	}
}

// TestSelectInputsEmpty: no candidates → empty (not nil-panic) result.
func TestSelectInputsEmpty(t *testing.T) {
	if got := selectInputs("proj_self", nil); len(got) != 0 {
		t.Fatalf("want empty, got %+v", got)
	}
}
