package document

import (
	"encoding/json"
	"fmt"
)

// AgentSettings is the JSON payload stored in a type=agent document's
// Structured field when the agent has Configuration-related defaults.
// The struct is intentionally narrow — additional agent settings join
// here as the substrate grows. story_fb600b97.
//
// DefaultConfigurationID, when non-nil, names a type=configuration
// document whose ContractRefs override the project default at
// workflow_claim time when a story has no configuration_id of its own
// AND the caller supplies the agent_id arg. Resolution precedence:
// per-call proposed_contracts → story.configuration_id →
// agent.default_configuration_id → project default.
type AgentSettings struct {
	DefaultConfigurationID *string `json:"default_configuration_id,omitempty"`
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
