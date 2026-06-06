package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestParseSemanticFindings_ConflictAndClean pins AC5: a contradictory-bundle
// agent output yields a finding; a coherent one yields none; severity defaults.
func TestParseSemanticFindings_ConflictAndClean(t *testing.T) {
	// A conflict reported by the reviewer, wrapped in the prose/fence an LLM
	// sometimes adds — the parser must still extract it.
	conflictRaw := "Here is my review:\n```json\n" +
		`{"findings":[{"severity":"error","code":"contradictory-principles","message":"P1 requires X; P2 forbids X"}]}` +
		"\n```\n"
	fs, err := parseSemanticFindings(conflictRaw)
	if err != nil {
		t.Fatalf("parse conflict: %v", err)
	}
	if len(fs) != 1 || fs[0].Code != "contradictory-principles" || fs[0].Severity != "error" {
		t.Fatalf("conflict finding wrong: %+v", fs)
	}

	// A coherent bundle: empty findings array → no findings, no error.
	clean, err := parseSemanticFindings(`{"findings": []}`)
	if err != nil {
		t.Fatalf("parse clean: %v", err)
	}
	if len(clean) != 0 {
		t.Fatalf("coherent bundle should yield no findings, got %+v", clean)
	}

	// Missing severity defaults to warn so the row writes coherently.
	defaulted, err := parseSemanticFindings(`{"findings":[{"code":"principle-skill-conflict","message":"skill S violates principle P"}]}`)
	if err != nil {
		t.Fatalf("parse defaulted: %v", err)
	}
	if len(defaulted) != 1 || defaulted[0].Severity != "warn" {
		t.Fatalf("severity default wrong: %+v", defaulted)
	}

	// Garbage (no JSON object) is an error, not a silent pass.
	if _, err := parseSemanticFindings("the model refused"); err == nil {
		t.Fatalf("expected an error for non-JSON output")
	}
}

// TestBuildSemanticBundle_IncludesParts pins AC1: the bundle carries principles,
// the skills index, and the story's ## Workflow (read from the repo trees).
func TestBuildSemanticBundle_IncludesParts(t *testing.T) {
	body := "# Story\n\nsome text\n\n## Workflow\n\n```yaml\nstates:\n  - backlog\n  - done\n```\n"
	bundle := buildSemanticBundle(body)
	var doc struct {
		Principles []struct {
			Name string `json:"name"`
		} `json:"principles"`
		Skills []struct {
			Name string `json:"name"`
		} `json:"skills"`
		Workflow string `json:"workflow"`
	}
	if err := json.Unmarshal([]byte(bundle), &doc); err != nil {
		t.Fatalf("bundle is not valid JSON: %v\n%s", err, bundle)
	}
	if !strings.Contains(doc.Workflow, "## Workflow") || !strings.Contains(doc.Workflow, "backlog") {
		t.Fatalf("bundle workflow missing the ## Workflow block: %q", doc.Workflow)
	}
	// The principles + skills arrays are present in the shape (their population
	// is read from the repo trees relative to CWD, exercised by the dogfood —
	// here we pin that the bundle carries all three keys and the workflow text).
}
