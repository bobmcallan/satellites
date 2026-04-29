package configseed

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

// sampleConfigurationMD references the contract by the same name as
// `sampleContractMD` plus the six lifecycle contract names a real
// system_default Configuration would carry. The test only seeds
// test_contract so refs to it resolve; this lets the configuration
// phase exercise its name→ID lookup without dragging in the full
// lifecycle contract set.
const sampleConfigurationMD = `---
name: test_configuration
contract_refs:
  - test_contract
skill_refs: []
principle_refs: []
tags: [test]
---
# Test Configuration

A minimal scope=system Configuration referencing one seeded contract.
`

// TestRun_LoadsSystemDefaultConfiguration covers story_764726d3 ACs 2,3,5:
// the loader ingests configurations/, resolves contract_refs by name to
// the just-seeded contract IDs, upserts a scope=system type=configuration
// doc, and is idempotent on re-run.
func TestRun_LoadsSystemDefaultConfiguration(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "agents/test_agent.md", sampleAgentMD)
	writeFile(t, dir, "contracts/test_contract.md", sampleContractMD)
	writeFile(t, dir, "workflows/test_workflow.md", sampleWorkflowMD)
	writeFile(t, dir, "configurations/test_configuration.md", sampleConfigurationMD)

	docs := document.NewMemoryStore()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	summary, err := Run(context.Background(), docs, dir, "wksp_sys", "system", now)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.Loaded != 4 {
		t.Errorf("loaded = %d, want 4 (agent + contract + workflow + configuration)", summary.Loaded)
	}
	if summary.Created != 4 {
		t.Errorf("created = %d, want 4", summary.Created)
	}
	if len(summary.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", summary.Errors)
	}

	// AC 3: exactly one type=configuration scope=system doc with the
	// expected name and resolved ContractRefs.
	configs, err := docs.List(context.Background(), document.ListOptions{
		Type:  document.TypeConfiguration,
		Scope: document.ScopeSystem,
		Limit: 50,
	}, nil)
	if err != nil {
		t.Fatalf("List configurations: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("configurations count = %d, want 1", len(configs))
	}
	cfgDoc := configs[0]
	if cfgDoc.Name != "test_configuration" {
		t.Errorf("name = %q, want test_configuration", cfgDoc.Name)
	}
	if cfgDoc.Scope != document.ScopeSystem {
		t.Errorf("scope = %q, want system", cfgDoc.Scope)
	}
	resolved, err := document.UnmarshalConfiguration(cfgDoc.Structured)
	if err != nil {
		t.Fatalf("unmarshal Configuration: %v", err)
	}
	if len(resolved.ContractRefs) != 1 {
		t.Fatalf("ContractRefs len = %d, want 1", len(resolved.ContractRefs))
	}
	contractDoc, err := docs.GetByName(context.Background(), "", "test_contract", nil)
	if err != nil {
		t.Fatalf("GetByName test_contract: %v", err)
	}
	if resolved.ContractRefs[0] != contractDoc.ID {
		t.Errorf("ContractRefs[0] = %q, want %q (resolved by name)", resolved.ContractRefs[0], contractDoc.ID)
	}

	// AC 5: re-run is idempotent.
	second, err := Run(context.Background(), docs, dir, "wksp_sys", "system", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second.Created != 0 {
		t.Errorf("second Run created %d, want 0", second.Created)
	}
	if second.Updated != 0 {
		t.Errorf("second Run updated %d, want 0", second.Updated)
	}
	configs2, err := docs.List(context.Background(), document.ListOptions{
		Type:  document.TypeConfiguration,
		Scope: document.ScopeSystem,
		Limit: 50,
	}, nil)
	if err != nil {
		t.Fatalf("List configurations after re-run: %v", err)
	}
	if len(configs2) != 1 {
		t.Fatalf("configurations count after re-run = %d, want 1 (no duplicate)", len(configs2))
	}
}

// TestRun_ConfigurationFailsOnUnresolvedRef covers the negative path:
// a configuration whose contract_refs name is not seeded must surface
// a precise error per pr_evidence (fail loud, not silent).
func TestRun_ConfigurationFailsOnUnresolvedRef(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Note: NO contracts/ entry — the ref is unresolvable.
	writeFile(t, dir, "configurations/bad_configuration.md", `---
name: bad_configuration
contract_refs: [missing_contract]
---
# Bad
`)

	docs := document.NewMemoryStore()
	summary, err := Run(context.Background(), docs, dir, "wksp_sys", "system", time.Now().UTC())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(summary.Errors) == 0 {
		t.Fatal("expected error for unresolved contract ref; got none")
	}
	gotMissing := false
	for _, e := range summary.Errors {
		if strings.Contains(e.Reason, "missing_contract") && strings.Contains(e.Reason, "not seeded") {
			gotMissing = true
			break
		}
	}
	if !gotMissing {
		t.Errorf("error must name the unresolved ref; got %v", summary.Errors)
	}
}

// TestRun_RealSeedDirShipsSystemDefaultConfiguration covers AC 1+3+4
// against the actual config/seed/ checked into the repo: a fresh boot
// against the real seed dir produces exactly one scope=system
// type=configuration doc named system_default whose ContractRefs map to
// the six lifecycle contracts in workflow order.
func TestRun_RealSeedDirShipsSystemDefaultConfiguration(t *testing.T) {
	t.Parallel()
	// Resolve relative to this test file's location: ../../config/seed.
	seedDir, err := filepath.Abs(filepath.Join("..", "..", "config", "seed"))
	if err != nil {
		t.Fatalf("abs seed dir: %v", err)
	}
	if _, err := os.Stat(seedDir); err != nil {
		t.Fatalf("seed dir %q not found: %v", seedDir, err)
	}

	docs := document.NewMemoryStore()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	summary, err := Run(context.Background(), docs, seedDir, "wksp_sys", "system", now)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(summary.Errors) != 0 {
		t.Fatalf("unexpected errors loading real seed dir: %v", summary.Errors)
	}

	configs, err := docs.List(context.Background(), document.ListOptions{
		Type:  document.TypeConfiguration,
		Scope: document.ScopeSystem,
		Limit: 50,
	}, nil)
	if err != nil {
		t.Fatalf("List configurations: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("configurations count = %d, want 1", len(configs))
	}
	cfgDoc := configs[0]
	if cfgDoc.Name != "system_default" {
		t.Errorf("name = %q, want system_default", cfgDoc.Name)
	}

	resolved, err := document.UnmarshalConfiguration(cfgDoc.Structured)
	if err != nil {
		t.Fatalf("unmarshal Configuration: %v", err)
	}
	wantOrder := []string{"preplan", "plan", "develop", "push", "merge_to_main", "story_close"}
	if len(resolved.ContractRefs) != len(wantOrder) {
		t.Fatalf("ContractRefs len = %d, want %d", len(resolved.ContractRefs), len(wantOrder))
	}
	for i, refID := range resolved.ContractRefs {
		contractDoc, err := docs.GetByID(context.Background(), refID, nil)
		if err != nil {
			t.Fatalf("ContractRefs[%d]=%q not found: %v", i, refID, err)
		}
		if contractDoc.Name != wantOrder[i] {
			t.Errorf("ContractRefs[%d] resolves to %q, want %q (workflow order matters)", i, contractDoc.Name, wantOrder[i])
		}
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
