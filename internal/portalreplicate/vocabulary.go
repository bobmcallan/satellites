package portalreplicate

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bobmcallan/satellites/internal/document"
)

// Vocabulary maps natural-language aliases to canonical action types.
// Sty_088f6d5c: configuration-over-code mandate — adding a new alias
// is a config edit, not a Go change. The vocabulary is loaded from a
// document with type=replicate_vocabulary at boot.
type Vocabulary struct {
	aliases map[string]ActionType
}

// NewVocabulary returns a Vocabulary seeded with each canonical
// ActionType mapping to itself. Tests and the no-config-found
// fallback path use this so the runner stays usable even without a
// loaded vocabulary doc.
func NewVocabulary() *Vocabulary {
	v := &Vocabulary{aliases: make(map[string]ActionType)}
	for _, t := range []ActionType{
		ActionNavigate, ActionWaitVisible, ActionClick,
		ActionDOMSnapshot, ActionConsoleCapture, ActionScreenshot,
		ActionDOMVisible,
	} {
		v.aliases[string(t)] = t
	}
	return v
}

// Add registers an alias → canonical mapping. Aliases are
// lower-cased and trimmed; the canonical type must satisfy
// IsKnownAction. Returns an error so the loader can surface
// misconfigurations as ErrorEntries.
func (v *Vocabulary) Add(alias string, canonical ActionType) error {
	alias = strings.ToLower(strings.TrimSpace(alias))
	if alias == "" {
		return fmt.Errorf("vocabulary: empty alias")
	}
	if !IsKnownAction(canonical) {
		return fmt.Errorf("vocabulary: unknown canonical action %q for alias %q", canonical, alias)
	}
	v.aliases[alias] = canonical
	return nil
}

// Resolve returns the canonical ActionType for an alias. Returns
// the alias unchanged with ok=false when no mapping is registered —
// callers either reject or fall through to IsKnownAction.
func (v *Vocabulary) Resolve(alias string) (ActionType, bool) {
	if v == nil {
		return ActionType(alias), false
	}
	t, ok := v.aliases[strings.ToLower(strings.TrimSpace(alias))]
	return t, ok
}

// Aliases returns a copy of the alias → canonical map. Used by the
// MCP `replicate_vocabulary_get` tool (when added) and by tests.
func (v *Vocabulary) Aliases() map[string]ActionType {
	out := make(map[string]ActionType, len(v.aliases))
	for k, t := range v.aliases {
		out[k] = t
	}
	return out
}

// LoadFromDocument parses a replicate_vocabulary Document's
// Structured payload into a Vocabulary. The payload shape mirrors
// the configseed parser: { aliases: { "go-to": "navigate", "tap":
// "click", ... } }. Empty / missing payload yields a Vocabulary
// with only the canonical-self mappings.
func LoadFromDocument(doc document.Document) (*Vocabulary, error) {
	v := NewVocabulary()
	if doc.Type != document.TypeReplicateVocabulary {
		return v, fmt.Errorf("portalreplicate: document type %q is not %s", doc.Type, document.TypeReplicateVocabulary)
	}
	if len(doc.Structured) == 0 {
		return v, nil
	}
	var payload struct {
		Aliases map[string]string `json:"aliases"`
	}
	if err := json.Unmarshal(doc.Structured, &payload); err != nil {
		return v, fmt.Errorf("portalreplicate: parse vocabulary: %w", err)
	}
	for alias, canonical := range payload.Aliases {
		if err := v.Add(alias, ActionType(canonical)); err != nil {
			return v, err
		}
	}
	return v, nil
}
