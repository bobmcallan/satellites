package server

import (
	"bytes"
	"strings"
	"testing"
)

// TestWorkspaceDetailRendersProjectPhase guards the sty_1ec60215 regression: the
// repo-card template references {{.Phase}}, so workspaceProjectRow MUST carry a
// Phase field. html/template aborts execution on a missing struct field — which
// blanked the workspace-detail repos section after the first card. Execute the
// real template against the real view struct (one row with a phase, one without)
// and fail on any execution error. Without the Phase field this test fails to
// compile or execute — exactly the guard `go build` + the prior tests lacked.
func TestWorkspaceDetailRendersProjectPhase(t *testing.T) {
	data := workspaceDetailData{
		Title:     "t",
		Workspace: workspaceRow{ID: "wksp_x", Name: "ws", Status: "active"},
		Projects: []workspaceProjectRow{
			{ID: "proj_a", Name: "a", Type: "platform", Phase: "discovery", Status: "active", Role: "admin"},
			{ID: "proj_b", Name: "b", Type: "skills", Status: "active", Role: "read"}, // no phase
		},
	}
	var buf bytes.Buffer
	if err := workspaceDetailTmpl.Execute(&buf, data); err != nil {
		t.Fatalf("workspace_detail render: %v", err)
	}
	out := buf.String()
	// Both repos must render (the bug truncated the section after the first card).
	if !strings.Contains(out, "proj_a") || !strings.Contains(out, "proj_b") {
		t.Errorf("not all repo cards rendered:\n%s", out)
	}
	if !strings.Contains(out, "phase-chip") || !strings.Contains(out, "discovery") {
		t.Error("phase chip 'discovery' not rendered for the project with a phase")
	}
}

// TestProjectsPageRendersProjectPhase is the same guard for the /projects listing,
// whose projectRow view struct also references {{.Phase}}.
func TestProjectsPageRendersProjectPhase(t *testing.T) {
	data := projectsData{
		Title: "t",
		Projects: []projectRow{
			{ID: "proj_a", Name: "a", Type: "platform", Phase: "discovery", Status: "active"},
			{ID: "proj_b", Name: "b", Type: "skills", Status: "active"}, // no phase
		},
	}
	var buf bytes.Buffer
	if err := projectsTmpl.Execute(&buf, data); err != nil {
		t.Fatalf("projects render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "proj_a") || !strings.Contains(out, "proj_b") {
		t.Errorf("not all project cards rendered:\n%s", out)
	}
	if !strings.Contains(out, "phase-chip") || !strings.Contains(out, "discovery") {
		t.Error("phase chip 'discovery' not rendered for the project with a phase")
	}
}
