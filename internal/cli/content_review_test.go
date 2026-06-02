package cli

import "testing"

// TestReviewContent_RottingRef pins AC5: a bare concrete slug fails; template
// forms and prose pass; fenced code is exempt.
func TestReviewContent_RottingRef(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int // expected finding count
	}{
		{"concrete story slug", "see story:sty_be65b4dd for details", 1},
		{"concrete doc + proj", "doc_4dc59149 under proj_fc7d72d8", 2},
		{"template form passes", "see story:<id> and project:<id>", 0},
		{"prose passes", "the satellites auth identity model", 0},
		{"fenced code exempt", "```\nid := \"sty_be65b4dd\"\n```\nclean prose here", 0},
		{"slug outside fence after fence", "```\nx\n```\nsee sty_abcdef now", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := reviewContent(c.body)
			if len(got) != c.want {
				t.Errorf("findings = %d, want %d (%v)", len(got), c.want, got)
			}
		})
	}
}

// TestReviewSkillForKind pins the kind→skill mapping the upload uses.
func TestReviewSkillForKind(t *testing.T) {
	cases := map[string]string{
		"documents":  "document-review",
		"skills":     "skill-review",
		"principles": "principle-review",
	}
	for kind, want := range cases {
		if got := reviewSkillForKind(kind); got != want {
			t.Errorf("reviewSkillForKind(%q) = %q, want %q", kind, got, want)
		}
	}
}
