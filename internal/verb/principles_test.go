package verb

import (
	"context"
	"testing"

	"github.com/bobmcallan/satellites/internal/document"
)

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

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

// The sidecar is curated: every scope's filter must require BOTH the scope
// tag AND principles:always, so only must-reads ride along — never the whole
// corpus (epic:lean-substrate, sty_05794178).
func TestPrincipleSidecarFilter_RequiresAlwaysTag(t *testing.T) {
	cases := []struct {
		req       PrincipleScopeRequest
		wantScope document.Scope
	}{
		{PrincipleScopeRequest{Scope: PrincipleScopeGlobal}, document.ScopeSystem},
		{PrincipleScopeRequest{Scope: PrincipleScopeWorkspace, WorkspaceID: "w1"}, document.ScopeWorkspace},
		{PrincipleScopeRequest{Scope: PrincipleScopeProject, WorkspaceID: "w1", ProjectID: "p1"}, document.ScopeProject},
		{PrincipleScopeRequest{Scope: PrincipleScopeStory, WorkspaceID: "w1", ProjectID: "p1"}, document.ScopeProject},
	}
	for _, tc := range cases {
		f, ok, err := principleSidecarFilter(tc.req)
		if err != nil || !ok {
			t.Fatalf("scope %s: ok=%v err=%v, want ok=true err=nil", tc.req.Scope, ok, err)
		}
		if !hasTag(f.Tags, PrincipleTagAlways) {
			t.Errorf("scope %s: filter %v missing %q — the full corpus would ride along, not just the curated set", tc.req.Scope, f.Tags, PrincipleTagAlways)
		}
		if !hasTag(f.Tags, PrincipleTag(tc.req.Scope)) {
			t.Errorf("scope %s: filter %v missing the scope tag", tc.req.Scope, f.Tags)
		}
		if f.Scope != tc.wantScope {
			t.Errorf("scope %s: filter.Scope=%q want %q", tc.req.Scope, f.Scope, tc.wantScope)
		}
	}
}

// A request that lacks the ids its scope needs is skipped (ok=false), not an
// error — preserving the pre-curation behaviour.
func TestPrincipleSidecarFilter_SkipsWhenIdsMissing(t *testing.T) {
	for _, r := range []PrincipleScopeRequest{
		{Scope: PrincipleScopeWorkspace},                  // no workspace id
		{Scope: PrincipleScopeProject, WorkspaceID: "w1"}, // no project id
		{Scope: PrincipleScopeStory, WorkspaceID: "w1"},   // no project id
	} {
		if _, ok, err := principleSidecarFilter(r); ok || err != nil {
			t.Errorf("scope %s with missing ids: ok=%v err=%v, want ok=false err=nil", r.Scope, ok, err)
		}
	}
}

func TestPrincipleSidecarFilter_UnknownScope(t *testing.T) {
	if _, ok, err := principleSidecarFilter(PrincipleScopeRequest{Scope: "bogus"}); ok || err == nil {
		t.Errorf("unknown scope: ok=%v err=%v, want ok=false err!=nil", ok, err)
	}
}
