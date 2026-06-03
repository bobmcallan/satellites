package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestEvaluateDeployGate(t *testing.T) {
	tests := []struct {
		name    string
		stories []gateStory
		wantOK  bool
		wantIn  string // substring the report must contain
	}{
		{
			name:    "all done — allowed",
			stories: []gateStory{{ID: "sty_a", Type: "story", Status: "done", Found: true}, {ID: "sty_b", Type: "story", Status: "done", Found: true}},
			wantOK:  true,
			wantIn:  "deploy allowed",
		},
		{
			name:    "one backlog — refused",
			stories: []gateStory{{ID: "sty_a", Type: "story", Status: "done", Found: true}, {ID: "sty_b", Type: "story", Status: "backlog", Found: true}},
			wantOK:  false,
			wantIn:  "status=backlog",
		},
		{
			name:    "in_progress — refused",
			stories: []gateStory{{ID: "sty_a", Type: "story", Status: "in_progress", Found: true}},
			wantOK:  false,
			wantIn:  "REFUSED",
		},
		{
			name:    "unfound ref — refused",
			stories: []gateStory{{ID: "sty_dead", Found: false}},
			wantOK:  false,
			wantIn:  "no such story",
		},
		{
			name:    "non-story type — refused",
			stories: []gateStory{{ID: "doc_x", Type: "document", Status: "done", Found: true}},
			wantOK:  false,
			wantIn:  "not a story",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ok, report := evaluateDeployGate(tc.stories)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v\nreport:\n%s", ok, tc.wantOK, report)
			}
			if !strings.Contains(report, tc.wantIn) {
				t.Fatalf("report missing %q:\n%s", tc.wantIn, report)
			}
		})
	}
}

func TestRunStoryGate(t *testing.T) {
	t.Run("refusal returns error and prints report", func(t *testing.T) {
		fetch := func(_ context.Context, id string) (gateStory, error) {
			if id == "sty_ok" {
				return gateStory{ID: id, Type: "story", Status: "done", Found: true}, nil
			}
			return gateStory{ID: id, Type: "story", Status: "backlog", Found: true}, nil
		}
		var out strings.Builder
		err := runStoryGate(context.Background(), []string{"sty_ok", "sty_bad"}, fetch, &out)
		if err == nil {
			t.Fatal("expected refusal error, got nil")
		}
		if !strings.Contains(out.String(), "sty_bad") {
			t.Fatalf("report missing blocked story:\n%s", out.String())
		}
	})

	t.Run("all gated returns nil", func(t *testing.T) {
		fetch := func(_ context.Context, id string) (gateStory, error) {
			return gateStory{ID: id, Type: "story", Status: "done", Found: true}, nil
		}
		var out strings.Builder
		if err := runStoryGate(context.Background(), []string{"sty_a"}, fetch, &out); err != nil {
			t.Fatalf("expected pass, got %v", err)
		}
	})

	t.Run("fetch transport error aborts (never passes on partial read)", func(t *testing.T) {
		fetch := func(_ context.Context, _ string) (gateStory, error) {
			return gateStory{}, errors.New("dial tcp: connection refused")
		}
		var out strings.Builder
		if err := runStoryGate(context.Background(), []string{"sty_a"}, fetch, &out); err == nil {
			t.Fatal("expected transport error to abort, got nil")
		}
	})

	t.Run("no ids is an error", func(t *testing.T) {
		fetch := func(_ context.Context, _ string) (gateStory, error) { return gateStory{}, nil }
		if err := runStoryGate(context.Background(), nil, fetch, &strings.Builder{}); err == nil {
			t.Fatal("expected error for empty id list")
		}
	})
}

func TestIsNotFound(t *testing.T) {
	if !isNotFound(errors.New("document_get: story not found")) {
		t.Error("want not-found for 'not found' message")
	}
	if isNotFound(errors.New("dial tcp: connection refused")) {
		t.Error("transport error must NOT read as not-found")
	}
	if isNotFound(nil) {
		t.Error("nil is not not-found")
	}
}
