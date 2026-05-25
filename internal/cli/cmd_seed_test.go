package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bobmcallan/satellites/internal/verb"
)

// TestPlanSeedPush_LayoutShapes covers the dispatch contract: workspace
// files emit before project files, and only the two canonical shapes
// produce dispatches — strays are silently skipped.
func TestPlanSeedPush_LayoutShapes(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, ".satellites", "seeds")
	wsDir := filepath.Join(root, "wksp_abcd1234")
	pjDir := filepath.Join(wsDir, "proj_efgh5678")
	if err := os.MkdirAll(pjDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustWrite(t, filepath.Join(wsDir, "workspace.md"), "ws body")
	mustWrite(t, filepath.Join(pjDir, "project.md"), "pj body")
	// Strays the walker must ignore: misnamed file at the right depth,
	// and a file at an unexpected depth.
	mustWrite(t, filepath.Join(wsDir, "README.md"), "not a seed")
	mustWrite(t, filepath.Join(pjDir, "notes.md"), "not a seed")
	mustWrite(t, filepath.Join(root, "loose.md"), "wrong depth")

	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	targets, err := planSeedPush(seedsRoot)
	if err != nil {
		t.Fatalf("planSeedPush: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("expected 2 dispatches, got %d: %+v", len(targets), targets)
	}
	if targets[0].Kind != "workspace" || targets[1].Kind != "project" {
		t.Fatalf("expected workspace-then-project ordering, got %s %s", targets[0].Kind, targets[1].Kind)
	}
	var wsReq verb.WorkspaceSeedApplyRequest
	if err := json.Unmarshal(targets[0].VerbReq, &wsReq); err != nil {
		t.Fatalf("unmarshal ws req: %v", err)
	}
	if wsReq.WorkspaceID != "wksp_abcd1234" || wsReq.Body != "ws body" {
		t.Fatalf("workspace req mismatch: %+v", wsReq)
	}
	var pjReq verb.ProjectSeedApplyRequest
	if err := json.Unmarshal(targets[1].VerbReq, &pjReq); err != nil {
		t.Fatalf("unmarshal pj req: %v", err)
	}
	if pjReq.ProjectID != "proj_efgh5678" || pjReq.Body != "pj body" {
		t.Fatalf("project req mismatch: %+v", pjReq)
	}
}

// TestPlanSeedPush_MissingRootIsZeroResult — operators without any
// seeds in their repo see "no seeds found" not an error.
func TestPlanSeedPush_MissingRootIsZeroResult(t *testing.T) {
	tmp := t.TempDir()
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	targets, err := planSeedPush(seedsRoot)
	if err != nil {
		t.Fatalf("planSeedPush: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("expected zero targets, got %d", len(targets))
	}
}

// TestSummariseSeedResp confirms the response decoder distinguishes
// applied / no change / unknown shapes.
func TestSummariseSeedResp(t *testing.T) {
	cases := map[string]string{
		`{"applied":true}`:                       "applied",
		`{"applied":false,"reason":"no change"}`: "no change",
		`{"applied":false}`:                      "no change",
	}
	for in, want := range cases {
		got, err := summariseSeedResp([]byte(in))
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", in, err)
		}
		if got != want {
			t.Errorf("%s: got %q want %q", in, got, want)
		}
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
