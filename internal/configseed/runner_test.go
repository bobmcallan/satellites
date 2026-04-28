package configseed

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
)

const sampleAgentMD = `---
name: test_agent
permission_patterns:
  - "Read:**"
  - "Bash:git_status"
tags: [test]
---
# Test Agent

Body content for the test agent.
`

const sampleContractMD = `---
name: test_contract
category: develop
required_role: role_orchestrator
required_categories: [develop]
permitted_actions:
  - "Read:**"
evidence_required: |
  Build + test outputs.
validation_mode: llm
---
# Test Contract

Body content.
`

const sampleWorkflowMD = `---
name: test_workflow
required_slots:
  - { contract_name: preplan, required: true, min_count: 1, max_count: 1 }
  - { contract_name: develop, required: true, min_count: 1, max_count: 5 }
---
# Test Workflow

Two-slot demo.
`

// writeFile writes content to dir/relPath, creating the directory tree.
func writeFile(t *testing.T, dir, relPath, content string) {
	t.Helper()
	full := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestRun_CreatesAgentsContractsWorkflows covers AC1+AC2: the loader
// reads agents/, contracts/, workflows/ subdirs and Run upserts each
// into the document store.
func TestRun_CreatesAgentsContractsWorkflows(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "agents/test_agent.md", sampleAgentMD)
	writeFile(t, dir, "contracts/test_contract.md", sampleContractMD)
	writeFile(t, dir, "workflows/test_workflow.md", sampleWorkflowMD)

	docs := document.NewMemoryStore()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	summary, err := Run(context.Background(), docs, dir, "wksp_sys", "system", now)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.Loaded != 3 {
		t.Errorf("loaded = %d, want 3", summary.Loaded)
	}
	if summary.Created != 3 {
		t.Errorf("created = %d, want 3", summary.Created)
	}
	if len(summary.Errors) != 0 {
		t.Errorf("unexpected errors: %v", summary.Errors)
	}

	// Spot-check one document round-trip.
	got, err := docs.GetByName(context.Background(), "", "test_agent", nil)
	if err != nil {
		t.Fatalf("GetByName test_agent: %v", err)
	}
	if got.Type != document.TypeAgent {
		t.Errorf("type = %q, want %q", got.Type, document.TypeAgent)
	}
	if got.Scope != document.ScopeSystem {
		t.Errorf("scope = %q, want system", got.Scope)
	}
}

// TestRun_Idempotent covers AC2: a second Run pass with unchanged
// files produces zero creates/updates (body-hash convergence in
// document.Upsert).
func TestRun_Idempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "agents/test_agent.md", sampleAgentMD)
	writeFile(t, dir, "contracts/test_contract.md", sampleContractMD)
	writeFile(t, dir, "workflows/test_workflow.md", sampleWorkflowMD)

	docs := document.NewMemoryStore()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	if _, err := Run(context.Background(), docs, dir, "wksp_sys", "system", now); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	summary, err := Run(context.Background(), docs, dir, "wksp_sys", "system", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if summary.Created != 0 {
		t.Errorf("second Run created %d, want 0", summary.Created)
	}
	if summary.Updated != 0 {
		t.Errorf("second Run updated %d, want 0 (body unchanged)", summary.Updated)
	}
	if summary.Skipped != 3 {
		t.Errorf("second Run skipped %d, want 3", summary.Skipped)
	}
}

// TestRun_MissingDirIsNoOp covers the resilience path: missing seed
// dirs are not an error — Run reports zero loaded.
func TestRun_MissingDirIsNoOp(t *testing.T) {
	t.Parallel()
	docs := document.NewMemoryStore()
	summary, err := Run(context.Background(), docs, t.TempDir(), "wksp_sys", "system", time.Now().UTC())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.Loaded != 0 {
		t.Errorf("loaded = %d, want 0", summary.Loaded)
	}
}

// TestRun_BadFileRecordedAsError covers AC1's resilience: a malformed
// markdown file produces an error entry but does not abort sibling
// files.
func TestRun_BadFileRecordedAsError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "agents/good.md", sampleAgentMD)
	writeFile(t, dir, "agents/bad.md", "no frontmatter here\n")

	docs := document.NewMemoryStore()
	summary, err := Run(context.Background(), docs, dir, "wksp_sys", "system", time.Now().UTC())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.Created != 1 {
		t.Errorf("created = %d, want 1 (only good.md)", summary.Created)
	}
	if len(summary.Errors) != 1 {
		t.Errorf("errors = %d, want 1", len(summary.Errors))
	}
}

// TestResolveSeedDir_EnvOverride covers AC5: SATELLITES_SEED_DIR
// overrides the default path.
func TestResolveSeedDir_EnvOverride(t *testing.T) {
	t.Setenv(SeedDirEnv, "/custom/seed")
	if got := ResolveSeedDir(); got != "/custom/seed" {
		t.Errorf("ResolveSeedDir = %q, want /custom/seed", got)
	}
}

// TestResolveSeedDir_Default covers AC5 default path.
func TestResolveSeedDir_Default(t *testing.T) {
	t.Setenv(SeedDirEnv, "")
	if got := ResolveSeedDir(); got != DefaultSeedDir {
		t.Errorf("ResolveSeedDir = %q, want %q", got, DefaultSeedDir)
	}
}
