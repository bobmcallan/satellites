package contract

import "encoding/json"

// workflowDocStructured mirrors the JSON shape configseed serialises
// for type=workflow documents (see internal/configseed/parsers.go
// workflowToInput).
type workflowDocStructured struct {
	RequiredSlots []struct {
		ContractName string `json:"contract_name"`
		Required     bool   `json:"required"`
		MinCount     int    `json:"min_count"`
		MaxCount     int    `json:"max_count"`
	} `json:"required_slots"`
}

// SlotsFromWorkflowDocStructured decodes the `required_slots` array
// out of a type=workflow document's Structured payload into the
// internal Slot shape consumed by MergeSlots / WorkflowSpec.Validate.
// Returns nil on empty or unparseable input — never an error — so
// callers can union slots from many documents without aborting on a
// single bad row. story_f0a78759.
func SlotsFromWorkflowDocStructured(structured []byte) []Slot {
	if len(structured) == 0 {
		return nil
	}
	var ws workflowDocStructured
	if err := json.Unmarshal(structured, &ws); err != nil {
		return nil
	}
	out := make([]Slot, 0, len(ws.RequiredSlots))
	for _, slot := range ws.RequiredSlots {
		if slot.ContractName == "" {
			continue
		}
		out = append(out, Slot{
			ContractName: slot.ContractName,
			Required:     slot.Required,
			MinCount:     slot.MinCount,
			MaxCount:     slot.MaxCount,
		})
	}
	return out
}

// Source values for Slot.Source on resolved (merged) workflow specs.
// Single-layer slots carry the contributing layer; multi-layer slots
// (slots present at ≥2 scopes) carry SourceMerged.
const (
	SourceSystem    = "system"
	SourceWorkspace = "workspace"
	SourceProject   = "project"
	SourceUser      = "user"
	SourceMerged    = "merged"
)

// LayerSlots is one layer in the resolver chain. Source identifies the
// scope tier of the originating workflow document; Slots are the slots
// declared in that document's required_slots frontmatter.
type LayerSlots struct {
	Source string
	Slots  []Slot
}

// MergeSlots applies the additive override rule from the design doc
// `docs/architecture-orchestrator-driven-configuration.md` §1: the
// chain is system ⊕ workspace ⊕ project ⊕ user, deduplicated by
// contract_name. On conflict the stricter constraint wins:
//
//   - min_count: max across layers.
//   - max_count: when at least one layer carries a non-zero (bounded)
//     value, the smallest non-zero value wins; if every layer is zero
//     the merged max stays zero (treated as unbounded by upstream
//     validation).
//   - required: false→true upgradable, never downgradable.
//
// Each merged Slot.Source records the contributing layer; a slot
// present at ≥2 layers reports SourceMerged.
//
// Output ordering preserves first-occurrence across the layer chain in
// the order layers are passed in. Callers therefore pass system first,
// then workspace, then project, then user.
func MergeSlots(layers ...LayerSlots) WorkflowSpec {
	type entry struct {
		slot   Slot
		layers map[string]struct{}
	}
	merged := make(map[string]*entry)
	order := make([]string, 0)
	for _, layer := range layers {
		for _, s := range layer.Slots {
			if s.ContractName == "" {
				continue
			}
			cur, ok := merged[s.ContractName]
			if !ok {
				slot := s
				if slot.Source == "" {
					slot.Source = layer.Source
				}
				cur = &entry{slot: slot, layers: map[string]struct{}{layer.Source: {}}}
				merged[s.ContractName] = cur
				order = append(order, s.ContractName)
				continue
			}
			cur.layers[layer.Source] = struct{}{}
			if s.MinCount > cur.slot.MinCount {
				cur.slot.MinCount = s.MinCount
			}
			switch {
			case cur.slot.MaxCount == 0 && s.MaxCount > 0:
				cur.slot.MaxCount = s.MaxCount
			case s.MaxCount > 0 && s.MaxCount < cur.slot.MaxCount:
				cur.slot.MaxCount = s.MaxCount
			}
			if s.Required && !cur.slot.Required {
				cur.slot.Required = true
			}
		}
	}
	for _, name := range order {
		e := merged[name]
		if len(e.layers) > 1 {
			e.slot.Source = SourceMerged
		}
	}
	out := make([]Slot, 0, len(order))
	for _, name := range order {
		out = append(out, merged[name].slot)
	}
	return WorkflowSpec{Slots: out}
}

// SourceFor returns the Source recorded on the matching slot in spec,
// or empty string if no such slot exists. Used by the workflow_claim
// gate to name the offending tier in mandatory_slot_missing errors.
func SourceFor(spec WorkflowSpec, contractName string) string {
	for _, slot := range spec.Slots {
		if slot.ContractName == contractName {
			return slot.Source
		}
	}
	return ""
}
