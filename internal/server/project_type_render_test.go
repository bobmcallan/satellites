package server

import (
	"strings"
	"testing"
)

// TestProjectDetailRendersType is the deterministic server-render proof for sty_57acbe4e:
// a project's freeform `type` is rendered into the project page as server HTML
// ([data-field="project-type"]) — present in the first response, before any deferred
// script runs, so the user-facing render does NOT depend on JavaScript. This replaces the
// flaky dev-login→navigate chromedp assertion that TestProjectTypeAdmin carried (the
// documented project-detail session race) with a browser-free, CI-running invariant, per
// the project-detail-chromedp-session-race precedent.
func TestProjectDetailRendersType(t *testing.T) {
	withType := renderProjectDetail(t, projectDetailData{
		Project: projectRow{ID: "proj_x", Name: "typed", Type: "reference-corpus"},
	})
	if !strings.Contains(withType, `data-field="project-type"`) {
		t.Errorf("project page missing [data-field=project-type] when type is set")
	}
	if !strings.Contains(withType, "reference-corpus") {
		t.Errorf("project page did not render the type value")
	}

	// Empty type → the TYPE row is omitted (the {{if .Project.Type}} guard), distinct from
	// a set type — so a cleared type leaves no stale field.
	noType := renderProjectDetail(t, projectDetailData{
		Project: projectRow{ID: "proj_y", Name: "untyped"},
	})
	if strings.Contains(noType, `data-field="project-type"`) {
		t.Errorf("project page rendered the type row when type is empty")
	}
}
