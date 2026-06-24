package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTaskFile drops a .satellites/tasks/<name>.md beside the given config path.
func writeTaskFile(t *testing.T, cfgPath, name, frontmatterType string) {
	t.Helper()
	repo := filepath.Dir(filepath.Dir(cfgPath)) // <repo>/.satellites/satellites.toml → <repo>
	dir := filepath.Join(repo, ".satellites", "tasks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ntype: " + frontmatterType + "\nlevel: global\n---\n\n" +
		"# " + name + "\n\n## Action\nGenerate the codegraph for this repo.\n\n" +
		"## Output\nA type codegraph document in this project.\n\n## Verification\nThe graph node count is non-zero.\n"
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestPublishTask_DryRunStampsLibrary covers AC1: a task authored under
// .satellites/tasks/ publishes to scope:library, provenance-stamped — the same
// path skills use — and --dryrun dispatches nothing.
func TestPublishTask_DryRunStampsLibrary(t *testing.T) {
	cfg := writeToml(t, "project_id = \"proj_pub\"\n")
	writeTaskFile(t, cfg, "codegraph", "task")

	var buf bytes.Buffer
	if err := publishSkill(context.Background(), &buf, "codegraph", "tasks", cfg, "", true, false); err != nil {
		t.Fatalf("publish task dry-run: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "would publish proj_pub/codegraph") {
		t.Errorf("want library target proj_pub/codegraph, got %q", got)
	}
	if !strings.Contains(got, "provenance") {
		t.Errorf("want a provenance stamp line, got %q", got)
	}
	if !strings.Contains(got, "nothing dispatched") {
		t.Errorf("dry-run must dispatch nothing, got %q", got)
	}
}

// TestPublishTask_RejectsNonTask covers the type guard: only a type:task file
// publishes from tasks/ (a skill misfiled there is refused).
func TestPublishTask_RejectsNonTask(t *testing.T) {
	cfg := writeToml(t, "project_id = \"proj_pub\"\n")
	writeTaskFile(t, cfg, "misfiled", "skill")

	var buf bytes.Buffer
	err := publishSkill(context.Background(), &buf, "misfiled", "tasks", cfg, "", true, false)
	if err == nil {
		t.Fatal("a non-task file under tasks/ must be refused")
	}
	if !strings.Contains(err.Error(), "only a task can be published") {
		t.Errorf("want a type-mismatch error, got %v", err)
	}
}
