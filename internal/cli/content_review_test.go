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
		"documents":  "satellites-document-review",
		"skills":     "satellites-skill-review",
		"principles": "satellites-principle-review",
	}
	for kind, want := range cases {
		if got := reviewSkillForKind(kind); got != want {
			t.Errorf("reviewSkillForKind(%q) = %q, want %q", kind, got, want)
		}
	}
}

// TestReviewSkillSelfContained pins the repo-script-dependency rule
// (sty_26adfec7): a skill step shelling to an unversioned repo script is
// blocked — including inside code fences, where invocations live — while a
// verb-only body stays clean.
func TestReviewSkillSelfContained(t *testing.T) {
	fixture := "## Routine\n\n```bash\nscripts/record-ci-evidence.sh   # story id from HEAD\n```\n"
	got := reviewSkillSelfContained(fixture)
	if len(got) != 1 || got[0].Rule != "repo-script-dependency" {
		t.Errorf("fenced repo-script invocation must report repo-script-dependency: %v", got)
	}

	clean := "## Routine\n\n```bash\nsatellites code index\n```\nSee scripts in your PATH.\n"
	if got := reviewSkillSelfContained(clean); len(got) != 0 {
		t.Errorf("verb-only body must stay clean: %v", got)
	}
}
