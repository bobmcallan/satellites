package configseed

import (
	"context"
	"encoding/json"
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

// sampleContractMD intentionally carries a permitted_actions key in
// frontmatter so TestRun_ContractStructuredOmitsPermittedActions can
// assert the loader IGNORES it (story_b7bf3a5f). The substrate sources
// permission_patterns from the agent doc, not the contract.
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

// TestRun_AgentStructuredCarriesInstruction (story_b7bf3a5f AC2) — the
// agent document carries the `instruction` frontmatter key in its
// Structured payload alongside permission_patterns. This is the
// concrete vehicle for agent-level execution guidance now that the
// contract no longer carries it (see TestRun_ContractStructuredOmitsPermittedActions).
//
// The configseed loader's mergeFrontmatterIntoJSON preserves arbitrary
// non-AgentSettings keys (parsers.go:181), so adding `instruction:` to
// frontmatter Just Works without parser changes — this test guards
// against accidental regression.
func TestRun_AgentStructuredCarriesInstruction(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const agentWithInstructionMD = `---
name: test_agent_with_instruction
instruction: |
  This is the agent's execution guidance: do X, do not do Y.
permission_patterns:
  - "Read:**"
tags: [test]
---
# Test Agent

Body.
`
	writeFile(t, dir, "agents/test_agent_with_instruction.md", agentWithInstructionMD)

	docs := document.NewMemoryStore()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	if _, err := Run(context.Background(), docs, dir, "wksp_sys", "system", now); err != nil {
		t.Fatalf("Run: %v", err)
	}

	agentDoc, err := docs.GetByName(context.Background(), "", "test_agent_with_instruction", nil)
	if err != nil {
		t.Fatalf("GetByName test_agent_with_instruction: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(agentDoc.Structured, &payload); err != nil {
		t.Fatalf("decode agent Structured: %v", err)
	}
	instruction, ok := payload["instruction"].(string)
	if !ok {
		t.Fatalf("agent Structured missing instruction string: payload=%v", payload)
	}
	if !strings.Contains(instruction, "do X") {
		t.Errorf("instruction = %q, want substring 'do X' (round-trip from frontmatter)", instruction)
	}
	// Sanity: permission_patterns still present alongside.
	patterns, _ := payload["permission_patterns"].([]any)
	if len(patterns) == 0 {
		t.Errorf("agent Structured missing permission_patterns alongside instruction")
	}
}

// TestRun_RealSeedAgentsCarryInstruction (story_b7bf3a5f AC2) — every
// lifecycle agent shipped in config/seed/agents/ declares an
// `instruction` field, the canonical home for agent-level execution
// guidance now that contracts carry only audit shape.
func TestRun_RealSeedAgentsCarryInstruction(t *testing.T) {
	t.Parallel()
	seedDir, err := filepath.Abs(filepath.Join("..", "..", "config", "seed"))
	if err != nil {
		t.Fatalf("abs seed dir: %v", err)
	}
	docs := document.NewMemoryStore()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	if _, err := Run(context.Background(), docs, seedDir, "wksp_sys", "system", now); err != nil {
		t.Fatalf("Run real seed: %v", err)
	}
	for _, name := range []string{
		"preplan_agent", "plan_agent", "develop_agent",
		"push_agent", "merge_agent", "story_close_agent",
	} {
		agentDoc, err := docs.GetByName(context.Background(), "", name, nil)
		if err != nil {
			t.Errorf("GetByName %s: %v", name, err)
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(agentDoc.Structured, &payload); err != nil {
			t.Errorf("%s: decode Structured: %v", name, err)
			continue
		}
		instruction, ok := payload["instruction"].(string)
		if !ok || strings.TrimSpace(instruction) == "" {
			t.Errorf("%s: missing or empty `instruction` field in Structured payload", name)
		}
	}
}

// TestRun_ContractStructuredOmitsPermittedActions (story_b7bf3a5f AC1+5)
// — even when the seed file's frontmatter carries `permitted_actions`,
// the loader must NOT write it into the contract document's Structured
// payload. The action-claim path sources permission_patterns from the
// agent doc (story_b39b393f / story_cc55e093); the contract's field is
// dead data.
func TestRun_ContractStructuredOmitsPermittedActions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "contracts/test_contract.md", sampleContractMD)

	docs := document.NewMemoryStore()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	if _, err := Run(context.Background(), docs, dir, "wksp_sys", "system", now); err != nil {
		t.Fatalf("Run: %v", err)
	}

	contractDoc, err := docs.GetByName(context.Background(), "", "test_contract", nil)
	if err != nil {
		t.Fatalf("GetByName test_contract: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(contractDoc.Structured, &payload); err != nil {
		t.Fatalf("decode contract Structured: %v", err)
	}
	if _, has := payload["permitted_actions"]; has {
		t.Errorf("contract Structured carries permitted_actions key %v — story_b7bf3a5f drops the dead field; the action-claim path sources permission_patterns from the agent doc", payload["permitted_actions"])
	}
	// Sanity: contract-level fields preserved.
	for _, key := range []string{"category", "evidence_required", "validation_mode"} {
		if _, has := payload[key]; !has {
			t.Errorf("contract Structured missing %q (must be preserved)", key)
		}
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

// samplePrincipleMD covers the principle frontmatter shape per
// story_ac3dc4d0: id (documentation slug, not consumed by parser),
// name (friendly title, used by Upsert.GetByName), scope (system),
// tags (free-form). Body becomes Document.Body (the description).
const samplePrincipleMD = `---
id: pr_test_principle
name: Test principle for seed loader
scope: system
tags:
  - test
---
This is the description body of the test principle. It survives
through the loader unchanged and lands in Document.Body.
`

// TestPrincipleSeedLoad covers story_ac3dc4d0 ACs 1, 3, 4, 5, 6, 7:
// the principle phase loads `principles/*.md`, upserts as
// type=principle scope=system, and is idempotent on re-run. story_ac3dc4d0.
func TestPrincipleSeedLoad(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "principles/pr_test_principle.md", samplePrincipleMD)

	docs := document.NewMemoryStore()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)

	// First run: creates the principle doc.
	summary, err := Run(context.Background(), docs, dir, "wksp_sys", "system", now)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(summary.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", summary.Errors)
	}
	if summary.Created < 1 {
		t.Fatalf("created = %d, want >=1", summary.Created)
	}

	got, err := docs.GetByName(context.Background(), "", "Test principle for seed loader", nil)
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got.Type != document.TypePrinciple {
		t.Errorf("type = %q, want %q", got.Type, document.TypePrinciple)
	}
	if got.Scope != document.ScopeSystem {
		t.Errorf("scope = %q, want system", got.Scope)
	}
	if !strings.Contains(got.Body, "description body of the test principle") {
		t.Errorf("body did not survive: %q", got.Body)
	}

	// Second run: idempotent — body hash unchanged → 0 changes for this
	// principle (skipped count goes up).
	prevSkipped := summary.Skipped
	summary2, err := Run(context.Background(), docs, dir, "wksp_sys", "system", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Run (second): %v", err)
	}
	if len(summary2.Errors) != 0 {
		t.Fatalf("unexpected errors on re-run: %v", summary2.Errors)
	}
	if summary2.Created != 0 {
		t.Errorf("re-run created = %d, want 0", summary2.Created)
	}
	if summary2.Skipped <= prevSkipped {
		t.Errorf("re-run skipped = %d, want > prev %d (principle should have skipped)", summary2.Skipped, prevSkipped)
	}
}

// TestRun_RealSeedDirShipsAllPrinciples (story_ac3dc4d0 AC1+AC7+AC8):
// the on-disk config/seed/principles/ directory contains exactly 9
// markdown files, each loads cleanly into a type=principle scope=system
// document via the principle phase.
func TestRun_RealSeedDirShipsAllPrinciples(t *testing.T) {
	t.Parallel()
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
		t.Fatalf("unexpected errors: %v", summary.Errors)
	}

	principles, err := docs.List(context.Background(), document.ListOptions{
		Type:  document.TypePrinciple,
		Scope: document.ScopeSystem,
		Limit: 50,
	}, nil)
	if err != nil {
		t.Fatalf("List principles: %v", err)
	}
	if len(principles) != 9 {
		t.Fatalf("principles count = %d, want 9", len(principles))
	}

	wantNames := map[string]bool{
		"Agile smallest-change delivery":                         false,
		"Evidence must be verifiable":                            false,
		"Iterate locally, deploy once":                           false,
		"Lifecycle and project contract separation":              false,
		"No unrequested abstractions or backwards-compat layers": false,
		"Pipeline integrity":                                     false,
		"Process is trust":                                       false,
		"Quality over speed":                                     false,
		"Root cause, not hack":                                   false,
	}
	for _, p := range principles {
		if _, ok := wantNames[p.Name]; !ok {
			t.Errorf("unexpected principle name %q", p.Name)
			continue
		}
		wantNames[p.Name] = true
		if p.Body == "" {
			t.Errorf("principle %q has empty body", p.Name)
		}
	}
	for name, found := range wantNames {
		if !found {
			t.Errorf("expected principle %q not seeded", name)
		}
	}
}
