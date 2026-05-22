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
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/variable"
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
//
// OS / Arch / CurrentVersion are per-request system-variable inputs.
// The templating layer reads them via the system-variables resolver;
// callers that aren't rendering templates can leave them empty.
type DocumentGetRequest struct {
	Name           string `json:"name"`
	Scope          string `json:"scope"`
	WorkspaceID    string `json:"workspace_id,omitempty"`
	ProjectID      string `json:"project_id,omitempty"`
	Version        string `json:"version,omitempty"`
	Inherit        bool   `json:"inherit,omitempty"`
	OS             string `json:"os,omitempty"`
	Arch           string `json:"arch,omitempty"`
	CurrentVersion string `json:"current_version,omitempty"`
}

// DocumentGetResponse bundles the resolved document row + version slice.
// resolved_scope reports the scope the document was actually found at
// (relevant when inherit cascade kicks in).
//
// raw_body / rendered_body / unresolved_vars are populated only for
// single-version reads (latest or a specific version). version=all
// returns the chain verbatim; rendering each historical body would
// silently rewrite old text against today's variables, which is
// usually not what an operator viewing history wants.
type DocumentGetResponse struct {
	Document       document.Document  `json:"document"`
	Versions       []document.Version `json:"versions"`
	ResolvedScope  string             `json:"resolved_scope"`
	RawBody        string             `json:"raw_body,omitempty"`
	RenderedBody   string             `json:"rendered_body,omitempty"`
	UnresolvedVars []string           `json:"unresolved_vars,omitempty"`
}

// documentTemplateCache holds per-(document_id, version) parsed
// templates. Substrate contract: a (doc, version) body is immutable,
// so cached *Parsed values are valid for the binary's lifetime.
var documentTemplateCache document.Cache

// DocumentUpsertRequest is the input shape for document_upsert. Every
// call creates a new version row; the store layer never updates in
// place. scope=system is rejected (system-scope writes go through the
// internal seed path, not user-facing verbs).
type DocumentUpsertRequest struct {
	Name        string `json:"name"`
	Scope       string `json:"scope"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
	Body        string `json:"body"`
}

// DocumentUpsertResponse mirrors document_get's shape so callers can
// reuse parse paths: the new version row is returned alongside the
// document pointer.
type DocumentUpsertResponse struct {
	Document document.Document `json:"document"`
	Version  document.Version  `json:"version"`
}

// DocumentDeleteRequest is the input shape for document_delete. Same
// addressing model as upsert; delete is soft (appends a tombstone
// version) so version=all preserves the chain.
type DocumentDeleteRequest struct {
	Name        string `json:"name"`
	Scope       string `json:"scope"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
}

// DocumentDeleteResponse returns the tombstone version that was
// appended.
type DocumentDeleteResponse struct {
	Document document.Document `json:"document"`
	Version  document.Version  `json:"version"`
}

func init() {
	Register(&Verb{
		Name:        "document_get",
		Description: "Fetch a document by (scope, name) with version selection + inherit cascade.",
		Invoke:      invokeDocumentGet,
	})
	Register(&Verb{
		Name:        "document_upsert",
		Description: "Append a new version to a workspace/project-scoped document.",
		Invoke:      invokeDocumentUpsert,
	})
	Register(&Verb{
		Name:        "document_delete",
		Description: "Soft-delete a workspace/project-scoped document by appending a tombstone version.",
		Invoke:      invokeDocumentDelete,
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

	// Stamp per-request system-variable inputs onto ctx so the
	// templating resolver and the variable_get system terminator see
	// the same values.
	ctx = WithSystemVarContext(ctx, req.OS, req.Arch, req.CurrentVersion)

	cascade := buildResolutionChain(req.Name, scope, req.WorkspaceID, req.ProjectID, req.Inherit)
	for i, key := range cascade {
		if err := authorizeRead(ctx, key); err != nil {
			return nil, err
		}
		res, lookupErr := documentStore.Get(ctx, key, opts)
		if lookupErr == nil {
			return marshalDocumentGet(ctx, res, key, opts, &req)
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

func invokeDocumentUpsert(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if documentStore == nil {
		return nil, fmt.Errorf("document_upsert: store not configured")
	}
	var req DocumentUpsertRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("document_upsert: %w: %v", ErrBadRequest, err)
		}
	}
	if strings.TrimSpace(req.Name) == "" {
		return nil, fmt.Errorf("document_upsert: %w: name required", ErrBadRequest)
	}
	scope, err := parseScope(req.Scope)
	if err != nil {
		return nil, err
	}
	key := document.Key{Scope: scope, WorkspaceID: req.WorkspaceID, ProjectID: req.ProjectID, Name: req.Name}
	if err := authorizeWrite(ctx, key); err != nil {
		return nil, err
	}
	doc, v, err := documentStore.Upsert(ctx, document.UpsertInput{
		Key:       key,
		Body:      req.Body,
		CreatedBy: callerUserID(ctx),
	}, time.Now().UTC())
	if err != nil {
		return nil, mapStoreError(err, "document_upsert")
	}
	return json.Marshal(DocumentUpsertResponse{Document: doc, Version: v})
}

func invokeDocumentDelete(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if documentStore == nil {
		return nil, fmt.Errorf("document_delete: store not configured")
	}
	var req DocumentDeleteRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("document_delete: %w: %v", ErrBadRequest, err)
		}
	}
	if strings.TrimSpace(req.Name) == "" {
		return nil, fmt.Errorf("document_delete: %w: name required", ErrBadRequest)
	}
	scope, err := parseScope(req.Scope)
	if err != nil {
		return nil, err
	}
	key := document.Key{Scope: scope, WorkspaceID: req.WorkspaceID, ProjectID: req.ProjectID, Name: req.Name}
	if err := authorizeWrite(ctx, key); err != nil {
		return nil, err
	}
	doc, v, err := documentStore.Delete(ctx, key, callerUserID(ctx), false, time.Now().UTC())
	if err != nil {
		return nil, mapStoreError(err, "document_delete")
	}
	return json.Marshal(DocumentDeleteResponse{Document: doc, Version: v})
}

// authorizeWrite is the write-path twin of authorizeRead: system writes
// from user-facing verbs are flatly rejected, workspace/project writes
// require workspace membership. CLI-local in-process invocations
// (authStore unwired) skip the membership check; the store-layer
// ErrScopeReadonly still trips for scope=system regardless.
func authorizeWrite(ctx context.Context, key document.Key) error {
	if key.Scope == document.ScopeSystem {
		return fmt.Errorf("document write: %w: system scope is read-only via verbs", ErrForbidden)
	}
	if authStore == nil {
		return nil
	}
	u := auth.FromContext(ctx)
	if u == nil {
		return fmt.Errorf("document write: %w: bearer required for %s scope", ErrUnauthorized, key.Scope)
	}
	if workspaceStore == nil {
		return nil
	}
	if key.WorkspaceID == "" {
		return fmt.Errorf("document write: %w: %s scope requires workspace_id", ErrBadRequest, key.Scope)
	}
	if _, err := workspaceStore.GetRole(ctx, key.WorkspaceID, u.ID); err != nil {
		if errors.Is(err, workspace.ErrMemberNotFound) {
			return fmt.Errorf("document write: %w: user not a member of workspace %s", ErrForbidden, key.WorkspaceID)
		}
		return err
	}
	return nil
}

// mapStoreError translates store-layer sentinels into verb-layer ones
// so the exec transport can map to canonical HTTP statuses.
func mapStoreError(err error, prefix string) error {
	switch {
	case errors.Is(err, document.ErrScopeReadonly):
		return fmt.Errorf("%s: %w: %v", prefix, ErrForbidden, err)
	case errors.Is(err, document.ErrScopeMismatch):
		return fmt.Errorf("%s: %w: %v", prefix, ErrBadRequest, err)
	case errors.Is(err, document.ErrNotFound):
		return fmt.Errorf("%s: %w: %v", prefix, ErrNotFound, err)
	default:
		return err
	}
}

func marshalDocumentGet(ctx context.Context, res document.GetResult, key document.Key, opts document.GetOptions, req *DocumentGetRequest) (json.RawMessage, error) {
	if res.Versions == nil {
		res.Versions = []document.Version{}
	}
	resp := DocumentGetResponse{
		Document:      res.Document,
		Versions:      res.Versions,
		ResolvedScope: string(key.Scope),
	}
	// Templating only applies to single-version reads. version=all
	// returns the chain raw — rendering historical bodies against
	// today's variables silently rewrites the operator's past.
	if !opts.AllVersions && len(res.Versions) == 1 {
		v := res.Versions[0]
		parsed := documentTemplateCache.Get(res.Document.ID, v.Version, v.Body)
		resolver := newTemplateResolver(ctx, key, req)
		rendered, unresolved := parsed.Render(resolver)
		resp.RawBody = v.Body
		resp.RenderedBody = rendered
		if len(unresolved) > 0 {
			resp.UnresolvedVars = unresolved
		}
	}
	return json.Marshal(resp)
}

// newTemplateResolver builds the per-call resolver document_get uses
// for {{name}} substitution. Order (highest to lowest precedence):
//
//  1. system variables (computed, non-overridable)
//  2. project-scope variables  (when project_id+workspace_id supplied)
//  3. workspace-scope variables (when workspace_id supplied)
//
// System vars come first by design: a workspace operator setting a
// variable named "version" cannot shadow the platform's reported
// version. Unresolved names are surfaced on unresolved_vars; the call
// does not fail.
func newTemplateResolver(ctx context.Context, key document.Key, req *DocumentGetRequest) document.Resolver {
	wsID, pjID := req.WorkspaceID, req.ProjectID
	if wsID == "" && key.Scope != document.ScopeSystem {
		wsID = key.WorkspaceID
	}
	if pjID == "" && key.Scope == document.ScopeProject {
		pjID = key.ProjectID
	}
	return document.ResolverFunc(func(name string) (string, bool) {
		if v, ok := systemVariableResolve(ctx, name); ok {
			return v, true
		}
		if variableStore == nil {
			return "", false
		}
		if wsID != "" && pjID != "" {
			if v, err := variableStore.Get(ctx, variable.Key{
				Scope: variable.ScopeProject, WorkspaceID: wsID, ProjectID: pjID, Name: name,
			}); err == nil {
				return v.Value, true
			}
		}
		if wsID != "" {
			if v, err := variableStore.Get(ctx, variable.Key{
				Scope: variable.ScopeWorkspace, WorkspaceID: wsID, Name: name,
			}); err == nil {
				return v.Value, true
			}
		}
		return "", false
	})
}
