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

func TestVariableSet_RejectsSystem(t *testing.T) {
	prev := variableStore
	variableStore = &variable.Store{}
	defer func() { variableStore = prev }()
	_, err := Get("variable_set").Invoke(context.Background(),
		json.RawMessage(`{"name":"x","scope":"system","value":"y"}`))
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestVariableDelete_RejectsSystem(t *testing.T) {
	prev := variableStore
	variableStore = &variable.Store{}
	defer func() { variableStore = prev }()
	_, err := Get("variable_delete").Invoke(context.Background(),
		json.RawMessage(`{"name":"x","scope":"system"}`))
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
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
