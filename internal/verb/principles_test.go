package verb

import (
	"context"
	"testing"
)

func TestPrincipleTag(t *testing.T) {
	cases := []struct {
		scope PrincipleScope
		want  string
	}{
		{PrincipleScopeGlobal, "principles:global"},
		{PrincipleScopeWorkspace, "principles:workspace"},
		{PrincipleScopeProject, "principles:project"},
		{PrincipleScopeStory, "principles:story"},
	}
	for _, tc := range cases {
		if got := PrincipleTag(tc.scope); got != tc.want {
			t.Errorf("PrincipleTag(%s) = %q, want %q", tc.scope, got, tc.want)
		}
	}
}

func TestLoadPrinciples_NoStore(t *testing.T) {
	prev := documentStore
	documentStore = nil
	defer func() { documentStore = prev }()

	got := LoadPrinciples(context.Background(),
		PrincipleScopeRequest{Scope: PrincipleScopeGlobal},
	)
	if got != nil {
		t.Fatalf("expected nil when documentStore is unwired, got %v", got)
	}
}

func TestLoadPrinciples_NoRequests(t *testing.T) {
	if got := LoadPrinciples(context.Background()); got != nil {
		t.Fatalf("expected nil with empty request slice, got %v", got)
	}
}
