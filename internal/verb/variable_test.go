package verb

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/variable"
)

func TestVariableVerbs_Registered(t *testing.T) {
	for _, name := range []string{"variable_get", "variable_set", "variable_list", "variable_delete"} {
		if Get(name) == nil {
			t.Errorf("verb %q not registered", name)
		}
	}
}

func TestVariableSet_RejectsComputedSystemName(t *testing.T) {
	// sty_cc4c671a: scope='system' writes are allowed for stored knobs;
	// the rejection is now name-specific — only names served by the
	// computed resolver fail.
	prev := variableStore
	variableStore = &variable.Store{}
	defer func() { variableStore = prev }()
	SetSystemVariableResolver(
		func(_ context.Context, name string) (string, bool) {
			if name == "version" {
				return "v0.0.0", true
			}
			return "", false
		},
		func(context.Context) []string { return []string{"version"} },
	)
	defer SetSystemVariableResolver(nil, nil)
	_, err := Get("variable_set").Invoke(context.Background(),
		json.RawMessage(`{"name":"version","scope":"system","value":"forged"}`))
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden for computed name, got %v", err)
	}
	if !strings.Contains(err.Error(), "computed") {
		t.Fatalf("error message should mention 'computed', got %v", err)
	}
}

func TestVariableGet_BadInput(t *testing.T) {
	prev := variableStore
	variableStore = &variable.Store{}
	defer func() { variableStore = prev }()
	cases := []struct{ body, want string }{
		{`{}`, "name required"},
		{`{"name":"x"}`, "scope required"},
		{`{"name":"x","scope":"other"}`, "unknown scope"},
	}
	for _, tc := range cases {
		_, err := Get("variable_get").Invoke(context.Background(), json.RawMessage(tc.body))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("body=%s want %q got %v", tc.body, tc.want, err)
		}
		if !errors.Is(err, ErrBadRequest) {
			t.Fatalf("body=%s: error should wrap ErrBadRequest, got %v", tc.body, err)
		}
	}
}

func TestBuildVariableResolutionChain(t *testing.T) {
	cases := []struct {
		name       string
		scope      variable.Scope
		wsID, pjID string
		inherit    bool
		want       []variable.Scope
	}{
		{
			name: "project no inherit", scope: variable.ScopeProject, wsID: "w", pjID: "p", inherit: false,
			want: []variable.Scope{variable.ScopeProject},
		},
		{
			name: "project inherit", scope: variable.ScopeProject, wsID: "w", pjID: "p", inherit: true,
			want: []variable.Scope{variable.ScopeProject, variable.ScopeWorkspace, variable.ScopeSystem},
		},
		{
			name: "workspace inherit", scope: variable.ScopeWorkspace, wsID: "w", inherit: true,
			want: []variable.Scope{variable.ScopeWorkspace, variable.ScopeSystem},
		},
		{
			name: "system inherit no-op", scope: variable.ScopeSystem, inherit: true,
			want: []variable.Scope{variable.ScopeSystem},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chain := buildVariableResolutionChain("v", tc.scope, tc.wsID, tc.pjID, tc.inherit)
			if len(chain) != len(tc.want) {
				t.Fatalf("chain len: got %d want %d", len(chain), len(tc.want))
			}
			for i, k := range chain {
				if k.Scope != tc.want[i] {
					t.Fatalf("step %d scope=%s want %s", i, k.Scope, tc.want[i])
				}
			}
		})
	}
}

func TestSystemVariableResolver_Defaults(t *testing.T) {
	// Default resolver is empty — no system var resolves.
	if _, ok := systemVariableResolve(context.Background(), "version"); ok {
		t.Fatal("expected default resolver to return ok=false")
	}
}
