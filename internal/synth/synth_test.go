package synth

import (
	"context"
	"strings"
	"testing"
)

// fakeGenerator is a deterministic stand-in for a real model — the Generator
// seam makes the single-shot executor testable without the live API.
type fakeGenerator struct {
	lastPrompt string
	out        string
}

func (f *fakeGenerator) Generate(_ context.Context, prompt string) (string, error) {
	f.lastPrompt = prompt
	return f.out, nil
}

// GenerateFromCorpus runs the injected generator over the built task prompt; a
// fake generator proves the single-shot path deterministically (no store, no API).
func TestGenerateFromCorpus_FakeGenerator(t *testing.T) {
	fg := &fakeGenerator{out: "SUMMARY"}
	svc := NewObjectiveService(fg, nil) // docs nil — GenerateFromCorpus does not read the store
	got, err := svc.GenerateFromCorpus(context.Background(), "do the task", []CorpusDoc{{Name: "a", Body: "hello"}})
	if err != nil || got != "SUMMARY" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	if !strings.Contains(fg.lastPrompt, "do the task") || !strings.Contains(fg.lastPrompt, "## a") {
		t.Fatalf("prompt not built from spec+corpus: %q", fg.lastPrompt)
	}
	if _, err := svc.GenerateFromCorpus(context.Background(), "x", nil); err == nil {
		t.Fatal("want error on empty corpus")
	}
}

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
