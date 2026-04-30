package mcpserver

import (
	"context"
	"testing"
)

// TestEnforceScopedProject covers the four-way decision matrix for
// URL-scoped project_id enforcement: pass-through when unscoped, scope
// wins when no candidate, equal scope/candidate matches, mismatched
// pair is rejected. This is the foundation of sty_c975ebeb's
// cross-project guardrail.
func TestEnforceScopedProject(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		scoped        string
		candidate     string
		wantEffective string
		wantOK        bool
	}{
		{name: "no scope, no candidate", scoped: "", candidate: "", wantEffective: "", wantOK: true},
		{name: "no scope, candidate", scoped: "", candidate: "proj_a", wantEffective: "proj_a", wantOK: true},
		{name: "scope, no candidate (scope wins)", scoped: "proj_a", candidate: "", wantEffective: "proj_a", wantOK: true},
		{name: "scope, matching candidate", scoped: "proj_a", candidate: "proj_a", wantEffective: "proj_a", wantOK: true},
		{name: "scope, mismatched candidate (rejected)", scoped: "proj_a", candidate: "proj_b", wantEffective: "", wantOK: false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := withScopedProjectID(context.Background(), tc.scoped)
			got, ok := enforceScopedProject(ctx, tc.candidate)
			if got != tc.wantEffective || ok != tc.wantOK {
				t.Errorf("enforceScopedProject(scoped=%q, candidate=%q) = (%q, %v), want (%q, %v)",
					tc.scoped, tc.candidate, got, ok, tc.wantEffective, tc.wantOK)
			}
		})
	}
}

// TestScopedProjectIDFrom_Empty confirms an unscoped context returns
// the zero value rather than panicking on the type assertion.
func TestScopedProjectIDFrom_Empty(t *testing.T) {
	t.Parallel()
	if got := ScopedProjectIDFrom(context.Background()); got != "" {
		t.Errorf("ScopedProjectIDFrom on bare ctx = %q, want empty", got)
	}
}
