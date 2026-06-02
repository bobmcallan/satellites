package verb

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/document"
)

// TestRequireScopeKey pins the project-bound invariant (sty_2fa6f087 AC1/AC5):
// a project-scoped list needs project_id AND workspace_id; a workspace-scoped
// list needs workspace_id; system/empty scope are unconstrained.
func TestRequireScopeKey(t *testing.T) {
	cases := []struct {
		name, scope, ws, pj string
		wantErr             bool
	}{
		{"project no project_id", "project", "wksp_1", "", true},
		{"project no workspace_id", "project", "", "proj_1", true},
		{"project both present", "project", "wksp_1", "proj_1", false},
		{"workspace no workspace_id", "workspace", "", "", true},
		{"workspace present", "workspace", "wksp_1", "", false},
		{"system unconstrained", "system", "", "", false},
		{"empty scope unconstrained", "", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := requireScopeKey(c.scope, c.ws, c.pj)
			if c.wantErr && err == nil {
				t.Fatalf("expected error for scope=%q ws=%q pj=%q", c.scope, c.ws, c.pj)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.wantErr && !errors.Is(err, ErrBadRequest) {
				t.Errorf("error should wrap ErrBadRequest, got %v", err)
			}
		})
	}
}

// TestInvokeDocumentList_ProjectScopeRequiresProjectID pins AC1 through the
// verb: an empty project_id at project scope is refused before the store is
// ever queried (documentStore is a bare value with no DB — reaching List would
// panic, so a clean ErrBadRequest proves the guard fires first).
func TestInvokeDocumentList_ProjectScopeRequiresProjectID(t *testing.T) {
	prevDoc, prevAuth := documentStore, authStore
	documentStore, authStore = &document.Store{}, nil
	defer func() { documentStore, authStore = prevDoc, prevAuth }()

	_, err := Get("document_list").Invoke(context.Background(),
		json.RawMessage(`{"type":"skill","scope":"project"}`))
	if err == nil || !errors.Is(err, ErrBadRequest) {
		t.Fatalf("expected ErrBadRequest for empty project_id, got %v", err)
	}
	if !strings.Contains(err.Error(), "project_id") {
		t.Errorf("error should name project_id, got %v", err)
	}
}

// TestAuthorizeListScope_Stub pins AC3: with auth wired, a non-system scope
// requires a bearer; with auth unwired (in-process CLI) the check is skipped.
func TestAuthorizeListScope_Stub(t *testing.T) {
	prev := authStore
	defer func() { authStore = prev }()

	authStore = &auth.Store{}
	if err := authorizeListScope(context.Background(), document.ScopeProject, "wksp_1"); err == nil || !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("project scope without a bearer should be ErrUnauthorized, got %v", err)
	}
	if err := authorizeListScope(context.Background(), document.ScopeSystem, ""); err != nil {
		t.Errorf("system scope needs no bearer, got %v", err)
	}

	authStore = nil
	if err := authorizeListScope(context.Background(), document.ScopeProject, "wksp_1"); err != nil {
		t.Errorf("in-process (no authStore) must skip the check, got %v", err)
	}
}

func TestDocumentVerbs_Registered(t *testing.T) {
	for _, name := range []string{"document_get", "document_upsert", "document_delete"} {
		if Get(name) == nil {
			t.Errorf("verb %q not registered", name)
		}
	}
}

func TestDocumentGet_StoreNotConfigured(t *testing.T) {
	prev := documentStore
	documentStore = nil
	defer func() { documentStore = prev }()
	_, err := Get("document_get").Invoke(context.Background(), json.RawMessage(`{"name":"x","scope":"system"}`))
	if err == nil || !strings.Contains(err.Error(), "store not configured") {
		t.Fatalf("expected store-not-configured error, got %v", err)
	}
}

func TestDocumentGet_BadInput(t *testing.T) {
	prev := documentStore
	documentStore = &document.Store{}
	defer func() { documentStore = prev }()

	cases := []struct {
		body string
		want string
	}{
		{`{}`, "name or id required"},
		{`{"name":"x"}`, "scope required"},
		{`{"name":"x","scope":"other"}`, "unknown scope"},
		{`{"name":"x","scope":"system","version":"junk"}`, "version must be"},
		{`{"name":"x","scope":"system","version":"0"}`, "version must be"},
	}
	for _, tc := range cases {
		_, err := Get("document_get").Invoke(context.Background(), json.RawMessage(tc.body))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("body=%s: expected %q, got %v", tc.body, tc.want, err)
		}
		if err != nil && !errors.Is(err, ErrBadRequest) {
			t.Fatalf("body=%s: error should wrap ErrBadRequest, got %v", tc.body, err)
		}
	}
}

func TestBuildResolutionChain(t *testing.T) {
	cases := []struct {
		name       string
		scope      document.Scope
		wsID, pjID string
		userID     string
		inherit    bool
		want       []document.Scope
	}{
		{
			name:  "project no inherit",
			scope: document.ScopeProject, wsID: "wksp_1", pjID: "proj_1", inherit: false,
			want: []document.Scope{document.ScopeProject},
		},
		{
			name:  "project inherit cascades to workspace then system",
			scope: document.ScopeProject, wsID: "wksp_1", pjID: "proj_1", inherit: true,
			want: []document.Scope{document.ScopeProject, document.ScopeWorkspace, document.ScopeSystem},
		},
		{
			name:  "workspace inherit cascades to system",
			scope: document.ScopeWorkspace, wsID: "wksp_1", inherit: true,
			want: []document.Scope{document.ScopeWorkspace, document.ScopeSystem},
		},
		{
			name:  "system inherit is a no-op",
			scope: document.ScopeSystem, inherit: true,
			want: []document.Scope{document.ScopeSystem},
		},
		{
			name:  "project inherit with caller prepends user layer at highest precedence",
			scope: document.ScopeProject, wsID: "wksp_1", pjID: "proj_1", userID: "usr_1", inherit: true,
			want: []document.Scope{document.ScopeUser, document.ScopeProject, document.ScopeWorkspace, document.ScopeSystem},
		},
		{
			name:  "no inherit does not prepend user even with a caller",
			scope: document.ScopeProject, wsID: "wksp_1", pjID: "proj_1", userID: "usr_1", inherit: false,
			want: []document.Scope{document.ScopeProject},
		},
		{
			name:  "direct user-scope read resolves the single user key",
			scope: document.ScopeUser, userID: "usr_1", inherit: false,
			want: []document.Scope{document.ScopeUser},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chain := buildResolutionChain("doc", tc.scope, tc.wsID, tc.pjID, tc.userID, tc.inherit)
			if len(chain) != len(tc.want) {
				t.Fatalf("chain length: got %d want %d (%+v)", len(chain), len(tc.want), chain)
			}
			for i, got := range chain {
				if got.Scope != tc.want[i] {
					t.Fatalf("step %d: scope=%s want %s", i, got.Scope, tc.want[i])
				}
				if got.Name != "doc" {
					t.Fatalf("step %d: name=%q want doc", i, got.Name)
				}
				if got.Scope == document.ScopeUser && got.UserID != tc.userID {
					t.Fatalf("step %d: user key UserID=%q want %q", i, got.UserID, tc.userID)
				}
			}
		})
	}
}

func TestDocumentUpsert_RejectsSystem(t *testing.T) {
	prev := documentStore
	documentStore = &document.Store{}
	defer func() { documentStore = prev }()
	_, err := Get("document_upsert").Invoke(context.Background(),
		json.RawMessage(`{"name":"x","scope":"system","body":"y"}`))
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestDocumentDelete_RejectsSystem(t *testing.T) {
	prev := documentStore
	documentStore = &document.Store{}
	defer func() { documentStore = prev }()
	_, err := Get("document_delete").Invoke(context.Background(),
		json.RawMessage(`{"name":"x","scope":"system"}`))
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestParseVersionSelector(t *testing.T) {
	cases := []struct {
		in          string
		wantAll     bool
		wantVersion int
		wantErr     bool
	}{
		{"", false, 0, false},
		{"latest", false, 0, false},
		{"all", true, 0, false},
		{"1", false, 1, false},
		{"42", false, 42, false},
		{"0", false, 0, true},
		{"-1", false, 0, true},
		{"abc", false, 0, true},
		{"1.5", false, 0, true},
	}
	for _, tc := range cases {
		opts, err := parseVersionSelector(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("%q: expected error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q: unexpected error: %v", tc.in, err)
		}
		if opts.AllVersions != tc.wantAll || opts.Version != tc.wantVersion {
			t.Fatalf("%q: got %+v want all=%v version=%d", tc.in, opts, tc.wantAll, tc.wantVersion)
		}
	}
}
