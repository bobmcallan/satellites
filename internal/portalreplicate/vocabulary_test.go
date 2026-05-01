package portalreplicate

import (
	"encoding/json"
	"testing"

	"github.com/bobmcallan/satellites/internal/document"
)

// TestVocabulary_DefaultsToCanonical verifies a fresh vocabulary
// resolves every canonical action type to itself — the substrate
// usable without any config doc loaded.
func TestVocabulary_DefaultsToCanonical(t *testing.T) {
	t.Parallel()
	v := NewVocabulary()
	for _, want := range []ActionType{
		ActionNavigate, ActionWaitVisible, ActionClick,
		ActionDOMSnapshot, ActionConsoleCapture, ActionScreenshot,
		ActionDOMVisible,
	} {
		got, ok := v.Resolve(string(want))
		if !ok || got != want {
			t.Errorf("Resolve(%q) = (%q, %v), want (%q, true)", want, got, ok, want)
		}
	}
}

// TestVocabulary_AddAlias covers the alias path plus rejection of
// unknown canonicals — guards against a typo in the config file
// silently registering a no-op mapping.
func TestVocabulary_AddAlias(t *testing.T) {
	t.Parallel()
	v := NewVocabulary()
	if err := v.Add("tap", ActionClick); err != nil {
		t.Fatalf("Add(tap → click): %v", err)
	}
	if got, ok := v.Resolve("tap"); !ok || got != ActionClick {
		t.Errorf("Resolve(tap) = (%q, %v), want (click, true)", got, ok)
	}
	// Aliases are case-insensitive.
	if got, ok := v.Resolve("TAP"); !ok || got != ActionClick {
		t.Errorf("Resolve(TAP) = (%q, %v), want (click, true) — aliases must be case-insensitive", got, ok)
	}
	if err := v.Add("bogus", "no-such-action"); err == nil {
		t.Error("Add accepted unknown canonical action; should reject")
	}
	if err := v.Add("", ActionClick); err == nil {
		t.Error("Add accepted empty alias; should reject")
	}
}

// TestLoadFromDocument round-trips a structured payload through the
// loader. Validates the configseed → runtime path: the vocabulary
// document's JSON aliases populate the Vocabulary correctly.
func TestLoadFromDocument(t *testing.T) {
	t.Parallel()
	payload := map[string]any{
		"aliases": map[string]string{
			"go-to": "navigate",
			"tap":   "click",
			"see":   "wait_visible",
		},
	}
	structured, _ := json.Marshal(payload)
	doc := document.Document{
		Type:       document.TypeReplicateVocabulary,
		Name:       "default",
		Structured: structured,
	}
	v, err := LoadFromDocument(doc)
	if err != nil {
		t.Fatalf("LoadFromDocument: %v", err)
	}
	cases := map[string]ActionType{
		"go-to": ActionNavigate,
		"tap":   ActionClick,
		"see":   ActionWaitVisible,
		// Canonical-self mappings still resolve.
		"click": ActionClick,
	}
	for alias, want := range cases {
		got, ok := v.Resolve(alias)
		if !ok || got != want {
			t.Errorf("Resolve(%q) = (%q, %v), want (%q, true)", alias, got, ok, want)
		}
	}
}

// TestLoadFromDocument_RejectsWrongType protects against a caller
// passing the wrong document type — the loader returns the canonical-
// only fallback plus a non-nil error so misconfigurations are loud.
func TestLoadFromDocument_RejectsWrongType(t *testing.T) {
	t.Parallel()
	doc := document.Document{Type: document.TypePrinciple, Name: "default", Structured: []byte("{}")}
	v, err := LoadFromDocument(doc)
	if err == nil {
		t.Fatal("expected error on wrong document type")
	}
	if v == nil {
		t.Fatal("LoadFromDocument returned nil vocabulary on error; should fall back to canonical-only")
	}
	if got, ok := v.Resolve(string(ActionClick)); !ok || got != ActionClick {
		t.Errorf("fallback vocabulary does not resolve canonical %q", ActionClick)
	}
}
