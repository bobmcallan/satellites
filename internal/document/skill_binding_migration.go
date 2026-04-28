package document

import (
	"context"
	"strings"
	"time"

	"github.com/ternarybob/arbor"
)

// MigrateSkillContractBindings is the boot-time sweep that translates
// legacy skill→contract bindings into the agent.skill_refs shape used
// by story_b1108d4a's resolution path. For each system-scope skill
// document whose ContractBinding is non-empty, the helper:
//
//  1. Looks up the contract document the binding references and reads
//     its name (e.g. "develop").
//  2. Looks up the matching lifecycle agent named `<contract>_agent`
//     (e.g. "develop_agent") seeded by main.go::seedLifecycleAgents.
//  3. Merges the skill id into the agent's AgentSettings.SkillRefs
//     (no-op when already present).
//  4. Clears the skill's ContractBinding so subsequent reads route
//     through the agent path.
//
// Each step is idempotent. Mismatches (no matching agent) leave the
// skill untouched and log a warn — the binding stays as a tombstone
// for that case so a later targeted fix can move it.
//
// Returns the count of migrated skills + the count of skipped rows
// for audit. Per-row failures are warn-logged and don't abort the
// sweep.
func MigrateSkillContractBindings(ctx context.Context, store Store, logger arbor.ILogger, now time.Time) (migrated, skipped int) {
	if store == nil {
		return 0, 0
	}
	skills, err := store.List(ctx, ListOptions{Type: TypeSkill}, nil)
	if err != nil {
		logger.Warn().Str("error", err.Error()).Msg("skill binding migration: list skills failed")
		return 0, 0
	}
	for _, skill := range skills {
		if skill.Status != StatusActive {
			continue
		}
		if skill.ContractBinding == nil || *skill.ContractBinding == "" {
			continue
		}
		contractDoc, err := store.GetByID(ctx, *skill.ContractBinding, nil)
		if err != nil {
			logger.Warn().
				Str("skill_id", skill.ID).
				Str("contract_id", *skill.ContractBinding).
				Str("error", err.Error()).
				Msg("skill binding migration: contract lookup failed")
			skipped++
			continue
		}
		agentName := contractDoc.Name + "_agent"
		agentDoc, err := store.GetByName(ctx, "", agentName, nil)
		if err != nil {
			logger.Warn().
				Str("skill_id", skill.ID).
				Str("contract_name", contractDoc.Name).
				Str("expected_agent", agentName).
				Msg("skill binding migration: matching agent not found, skill left bound")
			skipped++
			continue
		}
		if err := mergeSkillRefIntoAgent(ctx, store, agentDoc, skill.ID, now); err != nil {
			logger.Warn().
				Str("skill_id", skill.ID).
				Str("agent_id", agentDoc.ID).
				Str("error", err.Error()).
				Msg("skill binding migration: agent skill_refs merge failed")
			skipped++
			continue
		}
		emptyBinding := ""
		if _, err := store.Update(ctx, skill.ID, UpdateFields{ContractBinding: &emptyBinding}, "system:skill-binding-migration", now, nil); err != nil {
			logger.Warn().
				Str("skill_id", skill.ID).
				Str("error", err.Error()).
				Msg("skill binding migration: clear binding failed")
			skipped++
			continue
		}
		migrated++
	}
	if migrated > 0 || skipped > 0 {
		logger.Info().Int("migrated", migrated).Int("skipped", skipped).Msg("skill binding migration done")
	}
	return migrated, skipped
}

// mergeSkillRefIntoAgent appends skillID to the agent's
// AgentSettings.SkillRefs slice if not already present and writes the
// updated payload back via Update. No-op when the ref is already there.
func mergeSkillRefIntoAgent(ctx context.Context, store Store, agentDoc Document, skillID string, now time.Time) error {
	settings, err := UnmarshalAgentSettings(agentDoc.Structured)
	if err != nil {
		return err
	}
	for _, existing := range settings.SkillRefs {
		if strings.TrimSpace(existing) == skillID {
			return nil
		}
	}
	settings.SkillRefs = append(settings.SkillRefs, skillID)
	payload, err := MarshalAgentSettings(settings)
	if err != nil {
		return err
	}
	if _, err := store.Update(ctx, agentDoc.ID, UpdateFields{Structured: &payload}, "system:skill-binding-migration", now, nil); err != nil {
		return err
	}
	return nil
}
