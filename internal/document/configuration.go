package document

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Configuration is the JSON payload stored in a type=configuration
// document's Structured field. story_d371f155 introduced it to bundle
// named refs to one ordered contract list (the workflow shape), a skill
// set, and a principle set so stories and agents can pick a preset
// instead of accepting the project default. Document.Name carries the
// configuration name; Document.Body carries the human-readable
// description.
//
// ContractRefs is order-significant: position i is phase i of the
// workflow shape the Configuration declares. SkillRefs and PrincipleRefs
// are unordered sets — duplicates are not enforced at the schema layer
// but the validator rejects ids that don't resolve to active documents
// of the expected type.
type Configuration struct {
	ContractRefs  []string `json:"contract_refs"`
	SkillRefs     []string `json:"skill_refs"`
	PrincipleRefs []string `json:"principle_refs"`
}

// MarshalConfiguration encodes c as the JSON payload suitable for
// Document.Structured. Empty slices marshal as `[]`, never null, so the
// store-side validator and downstream readers can rely on a stable shape.
func MarshalConfiguration(c Configuration) ([]byte, error) {
	if c.ContractRefs == nil {
		c.ContractRefs = []string{}
	}
	if c.SkillRefs == nil {
		c.SkillRefs = []string{}
	}
	if c.PrincipleRefs == nil {
		c.PrincipleRefs = []string{}
	}
	return json.Marshal(c)
}

// UnmarshalConfiguration decodes a Document.Structured payload into a
// Configuration. Returns an error when payload is empty or malformed —
// the type=configuration Validate branch already rejects empty payloads
// before write, so an empty payload here means a corrupted row.
func UnmarshalConfiguration(payload []byte) (Configuration, error) {
	if len(payload) == 0 {
		return Configuration{}, errors.New("configuration: empty structured payload")
	}
	var c Configuration
	if err := json.Unmarshal(payload, &c); err != nil {
		return Configuration{}, fmt.Errorf("configuration: decode structured payload: %w", err)
	}
	return c, nil
}
