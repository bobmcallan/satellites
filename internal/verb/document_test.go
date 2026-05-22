package verb

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/document"
)

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
		{`{}`, "name required"},
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chain := buildResolutionChain("doc", tc.scope, tc.wsID, tc.pjID, tc.inherit)
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
