package cli

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/verb"
)

// TestFormatStoryGet pins the server-side story view: every AC1 field prints,
// and the state actor is derived from the story's own embedded ## Workflow
// (sty_eb4876e9).
func TestFormatStoryGet(t *testing.T) {
	body := "# s\n\n## Workflow\n\n```yaml\nstates:\n  - backlog\n  - {name: doing, actor: executor}\n  - done\ntransitions:\n  - {from: backlog, to: doing, reviewer_skill: \"entry\"}\n  - {from: doing,   to: done,  reviewer_skill: \"exit\"}\n```\n"
	resp := verb.DocumentGetResponse{Document: document.Document{
		ID: "sty_t1", Name: "the-story", Status: "doing", Category: "feature",
		Priority: "medium", Tags: []string{"a", "b"}, ParentID: "sty_p1", Headline: "the headline",
	}}
	out := formatStoryGet(resp, body)
	for _, want := range []string{"sty_t1", "the-story", "doing", "actor: executor", "feature", "medium", "a, b", "sty_p1", "the headline"} {
		if !strings.Contains(out, want) {
			t.Errorf("story view missing %q:\n%s", want, out)
		}
	}

	// No embedded workflow (or no actor for the status) → actor dash.
	resp.Document.Status = "backlog"
	out = formatStoryGet(resp, "no workflow here")
	if !strings.Contains(out, "actor: -") {
		t.Errorf("missing workflow must render actor as dash:\n%s", out)
	}
}

// TestRenderStoryGet_Body pins sty_16b43fe0: --body appends the full story body
// after the metadata, and the default (no flag) output is byte-identical to the
// metadata block alone — so exposing the body cannot silently change existing
// callers' output.
func TestRenderStoryGet_Body(t *testing.T) {
	body := "# the plan\n\n## Workflow\n\n```yaml\nstates: [backlog, done]\n```\n"
	resp := verb.DocumentGetResponse{Document: document.Document{
		ID: "sty_t1", Name: "the-story", Status: "backlog", Category: "feature",
	}}

	// Default: byte-identical to formatStoryGet, no body.
	if got, want := renderStoryGet(resp, body, false), formatStoryGet(resp, body); got != want {
		t.Errorf("default output drifted from metadata block:\n got: %q\nwant: %q", got, want)
	}
	if strings.Contains(renderStoryGet(resp, body, false), "the plan") {
		t.Error("default output must not include the body")
	}

	// --body: metadata block is still a prefix, and the full body follows.
	withBody := renderStoryGet(resp, body, true)
	if !strings.HasPrefix(withBody, formatStoryGet(resp, body)) {
		t.Errorf("--body output must start with the metadata block:\n%s", withBody)
	}
	for _, want := range []string{"--- body ---", "# the plan", "## Workflow", "states: [backlog, done]"} {
		if !strings.Contains(withBody, want) {
			t.Errorf("--body output missing %q:\n%s", want, withBody)
		}
	}
}
