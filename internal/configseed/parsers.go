package configseed

import (
	"encoding/json"
	"fmt"

	"github.com/bobmcallan/satellites/internal/document"
)

// agentToInput builds a document.UpsertInput for a kind=agent file.
// Frontmatter carries the agent's structured payload directly:
// permission_patterns + skill_refs flow into AgentSettings; any other
// keys (e.g. permitted_roles, tool_ceiling for the orchestrator) are
// merged into the raw JSON structured payload alongside.
func agentToInput(fm Frontmatter, body []byte, workspaceID, actor string) (document.UpsertInput, error) {
	name := fm.String("name")
	if name == "" {
		return document.UpsertInput{}, fmt.Errorf("agent: name required")
	}
	// AgentSettings is the documented narrow subset; serialize that
	// first, then layer non-AgentSettings keys on top so callers like
	// the orchestrator (permitted_roles, tool_ceiling) round-trip too.
	settings := document.AgentSettings{
		PermissionPatterns: fm.StringSlice("permission_patterns"),
		SkillRefs:          fm.StringSlice("skill_refs"),
	}
	base, err := document.MarshalAgentSettings(settings)
	if err != nil {
		return document.UpsertInput{}, fmt.Errorf("agent %q: marshal: %w", name, err)
	}
	merged, err := mergeFrontmatterIntoJSON(base, fm, structuredAgentSkipKeys)
	if err != nil {
		return document.UpsertInput{}, fmt.Errorf("agent %q: merge: %w", name, err)
	}
	return document.UpsertInput{
		WorkspaceID: workspaceID,
		ProjectID:   nil,
		Type:        document.TypeAgent,
		Name:        name,
		Body:        body,
		Structured:  merged,
		Scope:       document.ScopeSystem,
		Tags:        appendDistinct(fm.StringSlice("tags"), "seed", "configseed"),
		Actor:       actor,
	}, nil
}

// contractToInput builds a document.UpsertInput for a kind=contract file.
//
// story_b7bf3a5f: contract documents carry only contract-level concerns —
// category, required_role, required_categories, evidence_required, and
// validation_mode. The historic `permitted_actions` field has been removed
// here because the action-claim path in
// internal/mcpserver/claim_handlers.go sources permission_patterns
// exclusively from the claiming agent document (story_b39b393f /
// story_cc55e093). Contract-side permitted_actions was dead data; this
// stops writing it into the Structured payload.
func contractToInput(fm Frontmatter, body []byte, workspaceID, actor string) (document.UpsertInput, error) {
	name := fm.String("name")
	if name == "" {
		return document.UpsertInput{}, fmt.Errorf("contract: name required")
	}
	payload := map[string]any{
		"category":            fm.String("category"),
		"required_role":       fm.String("required_role"),
		"required_categories": fm.StringSlice("required_categories"),
		"evidence_required":   fm.String("evidence_required"),
		"validation_mode":     fm.String("validation_mode"),
	}
	pruneEmpty(payload)
	structured, err := json.Marshal(payload)
	if err != nil {
		return document.UpsertInput{}, fmt.Errorf("contract %q: marshal: %w", name, err)
	}
	return document.UpsertInput{
		WorkspaceID: workspaceID,
		ProjectID:   nil,
		Type:        document.TypeContract,
		Name:        name,
		Body:        body,
		Structured:  structured,
		Scope:       document.ScopeSystem,
		Tags:        appendDistinct(fm.StringSlice("tags"), "seed", "configseed"),
		Actor:       actor,
	}, nil
}

// workflowSlot is one entry in workflowStructured.RequiredSlots.
type workflowSlot struct {
	ContractName string `json:"contract_name"`
	Required     bool   `json:"required"`
	MinCount     int    `json:"min_count"`
	MaxCount     int    `json:"max_count"`
}

// workflowToInput builds a document.UpsertInput for a kind=workflow file.
func workflowToInput(fm Frontmatter, body []byte, workspaceID, actor string) (document.UpsertInput, error) {
	name := fm.String("name")
	if name == "" {
		return document.UpsertInput{}, fmt.Errorf("workflow: name required")
	}
	rawSlots, _ := fm["required_slots"].([]any)
	slots := make([]workflowSlot, 0, len(rawSlots))
	for i, raw := range rawSlots {
		m := asMap(raw)
		if m == nil {
			return document.UpsertInput{}, fmt.Errorf("workflow %q: required_slots[%d] not a map", name, i)
		}
		slot := workflowSlot{
			ContractName: stringOf(m["contract_name"]),
			Required:     boolOf(m["required"]),
			MinCount:     intOf(m["min_count"]),
			MaxCount:     intOf(m["max_count"]),
		}
		if slot.ContractName == "" {
			return document.UpsertInput{}, fmt.Errorf("workflow %q: required_slots[%d] missing contract_name", name, i)
		}
		slots = append(slots, slot)
	}
	if len(slots) == 0 {
		return document.UpsertInput{}, fmt.Errorf("workflow %q: required_slots empty", name)
	}
	structured, err := json.Marshal(map[string]any{"required_slots": slots})
	if err != nil {
		return document.UpsertInput{}, fmt.Errorf("workflow %q: marshal: %w", name, err)
	}
	return document.UpsertInput{
		WorkspaceID: workspaceID,
		ProjectID:   nil,
		Type:        document.TypeWorkflow,
		Name:        name,
		Body:        body,
		Structured:  structured,
		Scope:       document.ScopeSystem,
		Tags:        appendDistinct(fm.StringSlice("tags"), "seed", "configseed"),
		Actor:       actor,
	}, nil
}

// configurationToInput builds a document.UpsertInput for a
// kind=configuration file. story_764726d3.
//
// The frontmatter carries `contract_refs`, `skill_refs`, and
// `principle_refs` as lists of *names*. Names resolve to document IDs
// via resolveContract / resolveSkill / resolvePrinciple — closures the
// runner injects after the agent/contract/workflow phases have produced
// the docs that back those names. Unresolved names produce an error so
// the caller logs a precise failure instead of silently skipping a ref.
func configurationToInput(
	fm Frontmatter,
	body []byte,
	resolveContract, resolveSkill, resolvePrinciple func(name string) (string, bool),
	workspaceID, actor string,
) (document.UpsertInput, error) {
	name := fm.String("name")
	if name == "" {
		return document.UpsertInput{}, fmt.Errorf("configuration: name required")
	}
	resolveAll := func(names []string, kind string, lookup func(string) (string, bool)) ([]string, error) {
		ids := make([]string, 0, len(names))
		for _, n := range names {
			id, ok := lookup(n)
			if !ok {
				return nil, fmt.Errorf("configuration %q: %s ref %q not seeded", name, kind, n)
			}
			ids = append(ids, id)
		}
		return ids, nil
	}
	contractIDs, err := resolveAll(fm.StringSlice("contract_refs"), "contract", resolveContract)
	if err != nil {
		return document.UpsertInput{}, err
	}
	skillIDs, err := resolveAll(fm.StringSlice("skill_refs"), "skill", resolveSkill)
	if err != nil {
		return document.UpsertInput{}, err
	}
	principleIDs, err := resolveAll(fm.StringSlice("principle_refs"), "principle", resolvePrinciple)
	if err != nil {
		return document.UpsertInput{}, err
	}
	cfg := document.Configuration{
		ContractRefs:  contractIDs,
		SkillRefs:     skillIDs,
		PrincipleRefs: principleIDs,
	}
	structured, err := document.MarshalConfiguration(cfg)
	if err != nil {
		return document.UpsertInput{}, fmt.Errorf("configuration %q: marshal: %w", name, err)
	}
	return document.UpsertInput{
		WorkspaceID: workspaceID,
		ProjectID:   nil,
		Type:        document.TypeConfiguration,
		Name:        name,
		Body:        body,
		Structured:  structured,
		Scope:       document.ScopeSystem,
		Tags:        appendDistinct(fm.StringSlice("tags"), "seed", "configseed"),
		Actor:       actor,
	}, nil
}

// helpToInput builds a document.UpsertInput for a kind=help file.
// Sibling story_cc5c67a9 owns the type=help discriminator + portal
// surface; the loader entry point lives here so the runner stays
// kind-agnostic.
func helpToInput(fm Frontmatter, body []byte, workspaceID, actor string) (document.UpsertInput, error) {
	slug := fm.String("slug")
	if slug == "" {
		return document.UpsertInput{}, fmt.Errorf("help: slug required")
	}
	title := fm.String("title")
	if title == "" {
		return document.UpsertInput{}, fmt.Errorf("help %q: title required", slug)
	}
	payload := map[string]any{
		"title": title,
		"slug":  slug,
		"order": intOf(fm["order"]),
	}
	structured, err := json.Marshal(payload)
	if err != nil {
		return document.UpsertInput{}, fmt.Errorf("help %q: marshal: %w", slug, err)
	}
	return document.UpsertInput{
		WorkspaceID: workspaceID,
		ProjectID:   nil,
		Type:        document.TypeHelp,
		Name:        slug,
		Body:        body,
		Structured:  structured,
		Scope:       document.ScopeSystem,
		Tags:        appendDistinct(fm.StringSlice("tags"), "seed", "configseed", "help"),
		Actor:       actor,
	}, nil
}

// structuredAgentSkipKeys is the frontmatter-key set already consumed
// by AgentSettings; mergeFrontmatterIntoJSON skips these to avoid
// double-encoding. "name" and "tags" are also out of band — they live
// on the Document, not the Structured payload.
var structuredAgentSkipKeys = map[string]struct{}{
	"name":                {},
	"tags":                {},
	"permission_patterns": {},
	"skill_refs":          {},
}

// mergeFrontmatterIntoJSON layers extra frontmatter keys on top of
// base. base is the canonical structured payload; non-skip frontmatter
// keys merge into it without overwriting existing values.
func mergeFrontmatterIntoJSON(base []byte, fm Frontmatter, skip map[string]struct{}) ([]byte, error) {
	out := map[string]any{}
	if len(base) > 0 {
		if err := json.Unmarshal(base, &out); err != nil {
			return nil, err
		}
	}
	for k, v := range fm {
		if _, omit := skip[k]; omit {
			continue
		}
		if _, exists := out[k]; exists {
			continue
		}
		out[k] = v
	}
	return json.Marshal(out)
}

// pruneEmpty drops zero-value entries from m so the marshalled JSON
// stays free of empty fields (better diff signal in stored payloads).
func pruneEmpty(m map[string]any) {
	for k, v := range m {
		switch typed := v.(type) {
		case string:
			if typed == "" {
				delete(m, k)
			}
		case []string:
			if len(typed) == 0 {
				delete(m, k)
			}
		case nil:
			delete(m, k)
		}
	}
}

func stringOf(v any) string {
	s, _ := v.(string)
	return s
}

func boolOf(v any) bool {
	b, _ := v.(bool)
	return b
}

// asMap normalises the various shapes YAML decoders use for nested
// objects (Frontmatter, map[string]any, map[any]any) into a single
// map[string]any. Returns nil when the value is not a map.
func asMap(v any) map[string]any {
	switch typed := v.(type) {
	case map[string]any:
		return typed
	case Frontmatter:
		return map[string]any(typed)
	case map[any]any:
		out := make(map[string]any, len(typed))
		for k, val := range typed {
			if ks, ok := k.(string); ok {
				out[ks] = val
			}
		}
		return out
	}
	return nil
}

func intOf(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

// appendDistinct merges extras into seed without introducing duplicates.
func appendDistinct(seed []string, extras ...string) []string {
	seen := make(map[string]struct{}, len(seed))
	out := make([]string, 0, len(seed)+len(extras))
	for _, s := range seed {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, s := range extras {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
