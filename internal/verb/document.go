// Document verbs — the documents-as-substrate read/write surface.
//
// document_get is the only verb registered here in story 2; upsert +
// delete arrive in story 3, variables in 4, templating in 5.
//
// Scope-aware auth:
//   - system    : any authenticated caller reads
//   - workspace : caller must be a member of the named workspace
//   - project   : caller must be a member of the project's workspace
//
// CLI-local invocations (no authStore wired) bypass the membership
// check; the satellites-server boot path that wires authStore also
// wires workspaceStore + projectStore, so an authenticated HTTP request
// goes through the full check.

package verb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/workspace"
)

var documentStore *document.Store

// SetDocumentStore wires the server's document.Store into the verb
// package. Called from cmd/satellites-server on boot.
func SetDocumentStore(s *document.Store) { documentStore = s }

// DocumentGetRequest is the input shape for document_get.
//
//	Version: "" or "latest" → most recent active version
//	         "<N>"          → that exact version row
//	         "all"          → every version, ascending
//
// Inherit=true performs project → workspace → system fallback when the
// named document doesn't exist at the requested scope.
type DocumentGetRequest struct {
	Name        string `json:"name"`
	Scope       string `json:"scope"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
	Version     string `json:"version,omitempty"`
	Inherit     bool   `json:"inherit,omitempty"`
}

// DocumentGetResponse bundles the resolved document row + version slice.
// resolved_scope reports the scope the document was actually found at
// (relevant when inherit cascade kicks in).
type DocumentGetResponse struct {
	Document      document.Document  `json:"document"`
	Versions      []document.Version `json:"versions"`
	ResolvedScope string             `json:"resolved_scope"`
}

func init() {
	Register(&Verb{
		Name:        "document_get",
		Description: "Fetch a document by (scope, name) with version selection + inherit cascade.",
		Invoke:      invokeDocumentGet,
	})
}

func invokeDocumentGet(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if documentStore == nil {
		return nil, fmt.Errorf("document_get: store not configured")
	}
	var req DocumentGetRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("document_get: %w: %v", ErrBadRequest, err)
		}
	}
	if strings.TrimSpace(req.Name) == "" {
		return nil, fmt.Errorf("document_get: %w: name required", ErrBadRequest)
	}
	scope, err := parseScope(req.Scope)
	if err != nil {
		return nil, err
	}

	opts, err := parseVersionSelector(req.Version)
	if err != nil {
		return nil, err
	}

	cascade := buildResolutionChain(req.Name, scope, req.WorkspaceID, req.ProjectID, req.Inherit)
	for i, key := range cascade {
		if err := authorizeRead(ctx, key); err != nil {
			return nil, err
		}
		res, lookupErr := documentStore.Get(ctx, key, opts)
		if lookupErr == nil {
			return marshalDocumentGet(res, key.Scope)
		}
		if !errors.Is(lookupErr, document.ErrNotFound) {
			return nil, lookupErr
		}
		if i == len(cascade)-1 {
			return nil, fmt.Errorf("document_get: %w: %s/%s", ErrNotFound, key.Scope, key.Name)
		}
	}
	return nil, fmt.Errorf("document_get: %w", ErrNotFound)
}

// buildResolutionChain returns the ordered list of Keys to probe. With
// inherit=false the slice is exactly one key. With inherit=true and a
// project-scope request, it is [project, workspace, system]. workspace
// inherit cascades to system; system requests never cascade.
func buildResolutionChain(name string, scope document.Scope, wsID, pjID string, inherit bool) []document.Key {
	chain := []document.Key{{Scope: scope, WorkspaceID: wsID, ProjectID: pjID, Name: name}}
	if !inherit {
		return chain
	}
	switch scope {
	case document.ScopeProject:
		if wsID != "" {
			chain = append(chain, document.Key{Scope: document.ScopeWorkspace, WorkspaceID: wsID, Name: name})
		}
		chain = append(chain, document.Key{Scope: document.ScopeSystem, Name: name})
	case document.ScopeWorkspace:
		chain = append(chain, document.Key{Scope: document.ScopeSystem, Name: name})
	}
	return chain
}

// parseScope turns the wire-string scope into the typed Scope value.
func parseScope(s string) (document.Scope, error) {
	switch strings.TrimSpace(s) {
	case "system":
		return document.ScopeSystem, nil
	case "workspace":
		return document.ScopeWorkspace, nil
	case "project":
		return document.ScopeProject, nil
	case "":
		return "", fmt.Errorf("document_get: %w: scope required", ErrBadRequest)
	default:
		return "", fmt.Errorf("document_get: %w: unknown scope %q", ErrBadRequest, s)
	}
}

// parseVersionSelector turns the "" / "latest" / "<N>" / "all" wire
// strings into a typed GetOptions value.
func parseVersionSelector(v string) (document.GetOptions, error) {
	v = strings.TrimSpace(v)
	if v == "" || v == "latest" {
		return document.GetOptions{}, nil
	}
	if v == "all" {
		return document.GetOptions{AllVersions: true}, nil
	}
	n, err := parsePositiveInt(v)
	if err != nil {
		return document.GetOptions{}, fmt.Errorf("document_get: %w: version must be 'latest', 'all', or a positive integer; got %q", ErrBadRequest, v)
	}
	return document.GetOptions{Version: n}, nil
}

func parsePositiveInt(s string) (int, error) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not a positive integer")
		}
		n = n*10 + int(r-'0')
		if n > 1_000_000 {
			return 0, fmt.Errorf("version out of range")
		}
	}
	if n == 0 {
		return 0, fmt.Errorf("must be positive")
	}
	return n, nil
}

// authorizeRead enforces the scope-mismatch rule from acceptance
// criterion 4: workspace/project reads from a caller that's not a
// member of the workspace return 403, not 404. CLI-local in-process
// invocations (no authStore configured) skip the check.
func authorizeRead(ctx context.Context, key document.Key) error {
	if authStore == nil {
		return nil
	}
	if key.Scope == document.ScopeSystem {
		return nil
	}
	u := auth.FromContext(ctx)
	if u == nil {
		return fmt.Errorf("document_get: %w: bearer required for %s scope", ErrUnauthorized, key.Scope)
	}
	if workspaceStore == nil {
		// Workspace store unwired: in-process tests that don't exercise
		// auth shouldn't fail closed. Server boot always wires it.
		return nil
	}
	wsID := key.WorkspaceID
	if wsID == "" {
		return fmt.Errorf("document_get: %w: %s scope requires workspace_id", ErrBadRequest, key.Scope)
	}
	if _, err := workspaceStore.GetRole(ctx, wsID, u.ID); err != nil {
		if errors.Is(err, workspace.ErrMemberNotFound) {
			return fmt.Errorf("document_get: %w: user not a member of workspace %s", ErrForbidden, wsID)
		}
		return err
	}
	return nil
}

func marshalDocumentGet(res document.GetResult, resolvedScope document.Scope) (json.RawMessage, error) {
	if res.Versions == nil {
		res.Versions = []document.Version{}
	}
	return json.Marshal(DocumentGetResponse{
		Document:      res.Document,
		Versions:      res.Versions,
		ResolvedScope: string(resolvedScope),
	})
}
