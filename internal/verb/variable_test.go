package verb

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/auth"
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

func TestVariableGetResponse_RevealGatesSecretValue(t *testing.T) {
	secret := variable.Variable{Name: "dev_login.password", Value: "p4ss", Secret: true}
	plain := variable.Variable{Name: "stories.page_size", Value: "25"}

	if got := variableGetResponse(secret, "system", false); got.Value != "" || !got.Secret {
		t.Fatalf("secret without reveal should redact: value=%q secret=%v", got.Value, got.Secret)
	}
	if got := variableGetResponse(secret, "system", true); got.Value != "p4ss" || !got.Secret {
		t.Fatalf("secret with reveal should expose plaintext: value=%q secret=%v", got.Value, got.Secret)
	}
	if got := variableGetResponse(plain, "system", false); got.Value != "25" || got.Secret {
		t.Fatalf("plain value should pass through: value=%q secret=%v", got.Value, got.Secret)
	}
}

func TestVariableGet_RevealRequiresAdmin(t *testing.T) {
	// A non-admin reveal is rejected before any store access — assert the
	// gate fires with an empty store and an authStore wired.
	prevVar, prevAuth := variableStore, authStore
	variableStore = &variable.Store{}
	authStore = &auth.Store{}
	defer func() { variableStore, authStore = prevVar, prevAuth }()

	ctx := auth.WithUser(context.Background(), &auth.User{ID: "usr_x", Role: auth.RoleUser})
	_, err := Get("variable_get").Invoke(ctx,
		json.RawMessage(`{"name":"dev_login.password","scope":"system","reveal":true}`))
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden for non-admin reveal, got %v", err)
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
		userID     string
		inherit    bool
		want       []variable.Scope
	}{
		{
			name: "project no inherit", scope: variable.ScopeProject, wsID: "w", pjID: "p", inherit: false,
			want: []variable.Scope{variable.ScopeProject},
		},
		{
			name: "project inherit (no caller)", scope: variable.ScopeProject, wsID: "w", pjID: "p", inherit: true,
			want: []variable.Scope{variable.ScopeProject, variable.ScopeWorkspace, variable.ScopeSystem},
		},
		{
			name: "project inherit prepends caller user layer", scope: variable.ScopeProject, wsID: "w", pjID: "p", userID: "u1", inherit: true,
			want: []variable.Scope{variable.ScopeUser, variable.ScopeProject, variable.ScopeWorkspace, variable.ScopeSystem},
		},
		{
			name: "workspace inherit", scope: variable.ScopeWorkspace, wsID: "w", inherit: true,
			want: []variable.Scope{variable.ScopeWorkspace, variable.ScopeSystem},
		},
		{
			name: "workspace inherit prepends caller user layer", scope: variable.ScopeWorkspace, wsID: "w", userID: "u1", inherit: true,
			want: []variable.Scope{variable.ScopeUser, variable.ScopeWorkspace, variable.ScopeSystem},
		},
		{
			name: "system inherit no-op", scope: variable.ScopeSystem, inherit: true,
			want: []variable.Scope{variable.ScopeSystem},
		},
		{
			name: "user scope resolves the single user key", scope: variable.ScopeUser, userID: "u1", inherit: true,
			want: []variable.Scope{variable.ScopeUser},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chain := buildVariableResolutionChain("v", tc.scope, tc.wsID, tc.pjID, tc.userID, tc.inherit)
			if len(chain) != len(tc.want) {
				t.Fatalf("chain len: got %d want %d (%v)", len(chain), len(tc.want), chain)
			}
			for i, k := range chain {
				if k.Scope != tc.want[i] {
					t.Fatalf("step %d scope=%s want %s", i, k.Scope, tc.want[i])
				}
				if k.Scope == variable.ScopeUser && k.UserID != tc.userID {
					t.Fatalf("step %d user layer UserID=%q want %q", i, k.UserID, tc.userID)
				}
			}
		})
	}
}

// TestVariableSet_UserScopeRequiresCaller asserts a user-scope write with no
// caller identity is refused before the store is touched (sty_6cdf1cd0 AC2).
func TestVariableSet_UserScopeRequiresCaller(t *testing.T) {
	prev := variableStore
	variableStore = &variable.Store{}
	defer func() { variableStore = prev }()

	_, err := Get("variable_set").Invoke(context.Background(),
		json.RawMessage(`{"name":"k","scope":"user","value":"v"}`))
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("expected ErrBadRequest for user write with no caller, got %v", err)
	}
	if !strings.Contains(err.Error(), "caller identity") {
		t.Fatalf("error should mention 'caller identity', got %v", err)
	}
}

// TestParseVariableScope_User asserts the wire string "user" parses to
// ScopeUser (sty_6cdf1cd0 AC2).
func TestParseVariableScope_User(t *testing.T) {
	s, err := parseVariableScope("user")
	if err != nil {
		t.Fatalf("parse user scope: %v", err)
	}
	if s != variable.ScopeUser {
		t.Fatalf("got %s want %s", s, variable.ScopeUser)
	}
}

func TestSystemVariableResolver_Defaults(t *testing.T) {
	// Default resolver is empty — no system var resolves.
	if _, ok := systemVariableResolve(context.Background(), "version"); ok {
		t.Fatal("expected default resolver to return ok=false")
	}
}

// TestVariableSet_ReservedNameRequiresSecret asserts a reserved credential
// name (gemini.api_key) cannot be written as a plain variable — the check
// fires before the store is touched (sty_a6983e32 AC2).
func TestVariableSet_ReservedNameRequiresSecret(t *testing.T) {
	prev := variableStore
	variableStore = &variable.Store{}
	defer func() { variableStore = prev }()

	_, err := Get("variable_set").Invoke(context.Background(),
		json.RawMessage(`{"name":"gemini.api_key","scope":"system","value":"sk-leak"}`))
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("expected ErrBadRequest for plain reserved name, got %v", err)
	}
	if !strings.Contains(err.Error(), "reserved credential") {
		t.Fatalf("error should mention 'reserved credential', got %v", err)
	}
}

// TestVariableSet_SecretRequiresAdmin asserts a secret write by a
// non-admin authenticated caller is refused before the store is touched
// (sty_a6983e32 AC2).
func TestVariableSet_SecretRequiresAdmin(t *testing.T) {
	prevVar := variableStore
	variableStore = &variable.Store{}
	defer func() { variableStore = prevVar }()
	SetAuthStore(&auth.Store{})
	defer SetAuthStore(nil)

	ctx := auth.WithUser(context.Background(), &auth.User{ID: "u1"}) // no admin role
	_, err := Get("variable_set").Invoke(ctx,
		json.RawMessage(`{"name":"test.secret","scope":"system","value":"v","secret":true}`))
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden for non-admin secret write, got %v", err)
	}
	if !strings.Contains(err.Error(), "admin") {
		t.Fatalf("error should mention 'admin', got %v", err)
	}
}
