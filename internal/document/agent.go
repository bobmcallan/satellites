package document

import (
	"encoding/json"
	"fmt"
)

// AgentSettings is the JSON payload stored in a type=agent document's
// Structured field. The struct is intentionally narrow — additional
// agent settings join here as the substrate grows.
//
// DefaultConfigurationID (story_fb600b97): when non-nil, names a
// type=configuration document whose ContractRefs override the project
// default at workflow_claim time when a story has no configuration_id
// of its own AND the caller supplies the agent_id arg. Resolution
// precedence: per-call proposed_contracts → story.configuration_id →
// agent.default_configuration_id → project default.
//
// PermissionPatterns (story_b19260d8): the action_claim patterns this
// agent grants when allocated to a contract instance — e.g.
// `["Edit:internal/portal/**", "Bash:go_test"]`. The hook resolves
// permitted tool calls against this list once the role-based-execution
// shift lands (story_b39b393f); today the field is informational +
// foreshadows the migration.
//
// SkillRefs (story_b19260d8): the document IDs of type=skill rows the
// agent should pull when invoked.
//
// Ephemeral + StoryID (story_b19260d8): mark an agent as story-scoped.
// The sweeper archives ephemeral agents whose owning story is done /
// cancelled past `SATELLITES_EPHEMERAL_AGENT_RETENTION_HOURS`.
type AgentSettings struct {
	DefaultConfigurationID *string  `json:"default_configuration_id,omitempty"`
	PermissionPatterns     []string `json:"permission_patterns,omitempty"`
	SkillRefs              []string `json:"skill_refs,omitempty"`
	Ephemeral              bool     `json:"ephemeral,omitempty"`
	StoryID                *string  `json:"story_id,omitempty"`
}

// MarshalAgentSettings encodes s as the JSON payload suitable for an
// agent document's Structured field. Returns an empty (`{}`) payload
// for a zero-value struct so the validator has a stable shape to read.
func MarshalAgentSettings(s AgentSettings) ([]byte, error) {
	return json.Marshal(s)
}

// UnmarshalAgentSettings decodes a Document.Structured payload into an
// AgentSettings. Empty payloads return a zero-value struct rather than
// an error — agent documents that don't carry any settings remain
// valid (this is unlike Configuration, which requires a non-empty
// payload by Validate's invariant).
func UnmarshalAgentSettings(payload []byte) (AgentSettings, error) {
	if len(payload) == 0 {
		return AgentSettings{}, nil
	}
	var s AgentSettings
	if err := json.Unmarshal(payload, &s); err != nil {
		return AgentSettings{}, fmt.Errorf("agent: decode structured payload: %w", err)
	}
	return s, nil
}
