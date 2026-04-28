package document

import (
	"context"
	"testing"
	"time"

	satarbor "github.com/bobmcallan/satellites/internal/arbor"
)

// TestMigrateSkillContractBindings verifies story_b1108d4a's boot-time
// migration: legacy contract-bound skills get stamped onto the
// matching lifecycle agent's skill_refs and have their binding
// cleared. Agents whose name matches `<contract_name>_agent` are the
// merge target.
func TestMigrateSkillContractBindings(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Now().UTC()
	store := NewMemoryStore()
	logger := satarbor.New("warn")

	contractDoc, err := store.Create(ctx, Document{
		Type:  TypeContract,
		Scope: ScopeSystem,
		Name:  "develop",
		Body:  "develop contract",
	}, now)
	if err != nil {
		t.Fatalf("seed contract: %v", err)
	}

	agentSettings, _ := MarshalAgentSettings(AgentSettings{
		PermissionPatterns: []string{"Edit:**"},
	})
	agentDoc, err := store.Create(ctx, Document{
		Type:       TypeAgent,
		Scope:      ScopeSystem,
		Name:       "develop_agent",
		Body:       "develop lifecycle agent",
		Structured: agentSettings,
	}, now)
	if err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	skillDoc, err := store.Create(ctx, Document{
		Type:            TypeSkill,
		Scope:           ScopeSystem,
		Name:            "golang-testing",
		Body:            "golang testing skill",
		ContractBinding: StringPtr(contractDoc.ID),
	}, now)
	if err != nil {
		t.Fatalf("seed skill: %v", err)
	}

	migrated, skipped := MigrateSkillContractBindings(ctx, store, logger, now)
	if migrated != 1 || skipped != 0 {
		t.Fatalf("MigrateSkillContractBindings = (%d, %d), want (1, 0)", migrated, skipped)
	}

	updatedAgent, err := store.GetByID(ctx, agentDoc.ID, nil)
	if err != nil {
		t.Fatalf("re-read agent: %v", err)
	}
	updatedSettings, err := UnmarshalAgentSettings(updatedAgent.Structured)
	if err != nil {
		t.Fatalf("decode agent settings: %v", err)
	}
	found := false
	for _, ref := range updatedSettings.SkillRefs {
		if ref == skillDoc.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("agent.skill_refs missing migrated skill id %q; got %v", skillDoc.ID, updatedSettings.SkillRefs)
	}

	updatedSkill, err := store.GetByID(ctx, skillDoc.ID, nil)
	if err != nil {
		t.Fatalf("re-read skill: %v", err)
	}
	if updatedSkill.ContractBinding != nil && *updatedSkill.ContractBinding != "" {
		t.Errorf("skill.contract_binding = %q, want cleared", *updatedSkill.ContractBinding)
	}

	// Idempotency: running again must produce no further migrations.
	migrated2, _ := MigrateSkillContractBindings(ctx, store, logger, now)
	if migrated2 != 0 {
		t.Errorf("second run migrated = %d, want 0 (idempotent)", migrated2)
	}
}

// TestMigrateSkillContractBindings_NoAgentMatch verifies the helper
// leaves a skill alone (and counts it as skipped) when no matching
// `<contract>_agent` exists. The skill keeps its binding so a later
// targeted fix can move it.
func TestMigrateSkillContractBindings_NoAgentMatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Now().UTC()
	store := NewMemoryStore()
	logger := satarbor.New("warn")

	contractDoc, err := store.Create(ctx, Document{
		Type:  TypeContract,
		Scope: ScopeSystem,
		Name:  "obscure_phase",
		Body:  "no matching agent",
	}, now)
	if err != nil {
		t.Fatalf("seed contract: %v", err)
	}
	skillDoc, err := store.Create(ctx, Document{
		Type:            TypeSkill,
		Scope:           ScopeSystem,
		Name:            "obscure-skill",
		Body:            "skill bound to obscure phase",
		ContractBinding: StringPtr(contractDoc.ID),
	}, now)
	if err != nil {
		t.Fatalf("seed skill: %v", err)
	}

	migrated, skipped := MigrateSkillContractBindings(ctx, store, logger, now)
	if migrated != 0 || skipped != 1 {
		t.Errorf("MigrateSkillContractBindings = (%d, %d), want (0, 1)", migrated, skipped)
	}

	stillBound, err := store.GetByID(ctx, skillDoc.ID, nil)
	if err != nil {
		t.Fatalf("re-read skill: %v", err)
	}
	if stillBound.ContractBinding == nil || *stillBound.ContractBinding != contractDoc.ID {
		t.Errorf("skill.contract_binding lost despite no agent match; got %v", stillBound.ContractBinding)
	}
}
