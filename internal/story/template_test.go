package story

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
)

// makeBugTemplateDoc constructs a story_template document mirroring the
// bug.md seed fixture, used by the evaluator tests below.
func makeBugTemplateDoc(t *testing.T) document.Document {
	t.Helper()
	payload := map[string]any{
		"category": "bug",
		"fields": []map[string]any{
			{"name": "repro", "type": "text", "required": true},
			{"name": "fix_commit", "type": "string", "required": false},
		},
		"hooks": map[string]any{
			"in_progress": map[string]any{
				"structured": []map[string]any{
					{"type": "field_present", "field": "repro"},
				},
				"natural_language": []string{"Repro must reproduce the bug."},
			},
			"done": map[string]any{
				"structured": []map[string]any{
					{"type": "field_present", "field": "fix_commit"},
					{"type": "ledger_evidence_present", "tag": "post-deploy"},
				},
			},
		},
	}
	structured, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return document.Document{
		Type:       document.TypeStoryTemplate,
		Name:       "bug",
		Structured: structured,
	}
}

// TestLoadTemplate covers the happy path: a well-formed
// story_template document parses into a populated Template.
func TestLoadTemplate(t *testing.T) {
	t.Parallel()
	doc := makeBugTemplateDoc(t)
	tmpl, err := LoadTemplate(doc)
	if err != nil {
		t.Fatalf("LoadTemplate: %v", err)
	}
	if tmpl.Category != "bug" {
		t.Errorf("category = %q, want bug", tmpl.Category)
	}
	if len(tmpl.Fields) != 2 || tmpl.Fields[0].Name != "repro" {
		t.Errorf("fields = %+v", tmpl.Fields)
	}
	if hook, ok := tmpl.Hooks["in_progress"]; !ok || len(hook.Structured) != 1 {
		t.Errorf("in_progress hook missing or malformed: %+v", tmpl.Hooks)
	}
}

// TestLoadTemplate_RejectsWrongType protects against accidental
// loading of the wrong document type — story_get and the lifecycle
// evaluator both rely on the type guard.
func TestLoadTemplate_RejectsWrongType(t *testing.T) {
	t.Parallel()
	doc := document.Document{Type: document.TypePrinciple, Name: "bug", Structured: []byte("{}")}
	if _, err := LoadTemplate(doc); err == nil {
		t.Fatal("LoadTemplate(non-story_template) returned no error")
	}
}

// TestEvaluateTransition_FieldPresent is the central enforcement
// guarantee: a hook of type field_present blocks the transition with
// a natural-language message when the field is empty, and passes when
// the field is populated.
func TestEvaluateTransition_FieldPresent(t *testing.T) {
	t.Parallel()
	tmpl, err := LoadTemplate(makeBugTemplateDoc(t))
	if err != nil {
		t.Fatalf("LoadTemplate: %v", err)
	}
	ev := EvaluationContext{
		LedgerEntriesForStory: func(context.Context, string) ([]ledger.LedgerEntry, error) {
			return nil, nil
		},
	}

	// repro empty → in_progress is blocked.
	st := Story{ID: "sty_demo", Category: "bug", Fields: map[string]any{}}
	failures := tmpl.EvaluateTransition(context.Background(), "in_progress", st, ev)
	if len(failures) == 0 {
		t.Fatal("expected at least one failure for empty repro")
	}
	if !strings.Contains(failures[0], "repro") {
		t.Errorf("failure should name the field: %q", failures[0])
	}

	// repro populated → in_progress passes.
	st.Fields["repro"] = "navigate /, click hamburger, expect dropdown"
	if failures := tmpl.EvaluateTransition(context.Background(), "in_progress", st, ev); len(failures) != 0 {
		t.Errorf("populated repro still failed: %v", failures)
	}
}

// TestEvaluateTransition_LedgerEvidence covers the evidence-presence
// check that closes the loop for bug stories: post-deploy evidence
// must be attached before the close transition is allowed.
func TestEvaluateTransition_LedgerEvidence(t *testing.T) {
	t.Parallel()
	tmpl, err := LoadTemplate(makeBugTemplateDoc(t))
	if err != nil {
		t.Fatalf("LoadTemplate: %v", err)
	}
	st := Story{
		ID:       "sty_demo",
		Category: "bug",
		Fields:   map[string]any{"fix_commit": "abc123"},
	}

	// No ledger entries yet → done is blocked.
	noEv := EvaluationContext{
		LedgerEntriesForStory: func(context.Context, string) ([]ledger.LedgerEntry, error) {
			return []ledger.LedgerEntry{}, nil
		},
	}
	failures := tmpl.EvaluateTransition(context.Background(), "done", st, noEv)
	if len(failures) == 0 {
		t.Fatal("expected ledger_evidence_present to block done")
	}

	// Add a row tagged post-deploy → done passes.
	ev := EvaluationContext{
		LedgerEntriesForStory: func(context.Context, string) ([]ledger.LedgerEntry, error) {
			return []ledger.LedgerEntry{
				{Tags: []string{"post-deploy", "agent"}},
			}, nil
		},
	}
	if failures := tmpl.EvaluateTransition(context.Background(), "done", st, ev); len(failures) != 0 {
		t.Errorf("post-deploy evidence present, still failed: %v", failures)
	}
}

// TestEvaluateTransition_UnknownCheck verifies the forward-compat slot:
// a check type the substrate doesn't recognise is treated as a pass,
// not a block. Lets future templates declare hooks the binary doesn't
// yet implement without bricking transitions on existing stories.
func TestEvaluateTransition_UnknownCheck(t *testing.T) {
	t.Parallel()
	payload := map[string]any{
		"category": "future",
		"fields":   []map[string]any{},
		"hooks": map[string]any{
			"done": map[string]any{
				"structured": []map[string]any{
					{"type": "agent_evaluates", "field": "anything"},
				},
			},
		},
	}
	structured, _ := json.Marshal(payload)
	doc := document.Document{
		Type:       document.TypeStoryTemplate,
		Name:       "future",
		Structured: structured,
	}
	tmpl, err := LoadTemplate(doc)
	if err != nil {
		t.Fatalf("LoadTemplate: %v", err)
	}
	st := Story{ID: "sty_demo", Category: "future", Fields: map[string]any{}}
	if failures := tmpl.EvaluateTransition(context.Background(), "done", st, EvaluationContext{}); len(failures) != 0 {
		t.Errorf("unknown check type blocked transition: %v", failures)
	}
}

// TestEvaluateTransition_NoHookForStatus is the missing-hook case: a
// transition the template doesn't speak to is a pass-through.
func TestEvaluateTransition_NoHookForStatus(t *testing.T) {
	t.Parallel()
	tmpl, err := LoadTemplate(makeBugTemplateDoc(t))
	if err != nil {
		t.Fatalf("LoadTemplate: %v", err)
	}
	st := Story{ID: "sty_demo", Category: "bug", Fields: map[string]any{}}
	if failures := tmpl.EvaluateTransition(context.Background(), "ready", st, EvaluationContext{}); len(failures) != 0 {
		t.Errorf("ready has no template hook, still got failures: %v", failures)
	}
}
