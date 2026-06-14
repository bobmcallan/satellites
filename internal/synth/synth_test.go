package synth

import "testing"

// BuildTaskPrompt envelopes an arbitrary spec + corpus the one shared way.
func TestBuildTaskPromptEnvelope(t *testing.T) {
	got := BuildTaskPrompt("  do the thing  ", []CorpusDoc{{Name: "a", Body: "  hello  "}, {Name: "b", Body: "world"}})
	want := "do the thing\n\n--- DOCUMENTS ---\n\n## a\nhello\n\n## b\nworld\n\n--- END DOCUMENTS ---\n"
	if got != want {
		t.Fatalf("BuildTaskPrompt mismatch:\n got=%q\nwant=%q", got, want)
	}
}

// The objective prompt must stay byte-identical to the pre-refactor envelope:
// the objective instruction, then the shared DOCUMENTS block (no behavioural
// change — the refactor only routes it through BuildTaskPrompt).
func TestBuildObjectivePromptUnchanged(t *testing.T) {
	corpus := []CorpusDoc{{Name: "a", Body: "  hello  "}}
	want := objectiveSpec + "\n\n--- DOCUMENTS ---\n\n## a\nhello\n\n--- END DOCUMENTS ---\n"
	if got := BuildObjectivePrompt(corpus); got != want {
		t.Fatalf("BuildObjectivePrompt changed:\n got=%q\nwant=%q", got, want)
	}
	if BuildObjectivePrompt(corpus) != BuildTaskPrompt(objectiveSpec, corpus) {
		t.Fatal("BuildObjectivePrompt must equal BuildTaskPrompt(objectiveSpec, …)")
	}
}
