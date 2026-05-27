// Variable verbs — the operator-set name/value substrate.
//
// Same scope auth model as documents:
//   - system    : reads from the computed-variables resolver (story 5)
//   - workspace : member of the workspace required
//   - project   : member of the project's workspace required
//
// System writes are rejected — system variables are computed per
// request, never table rows.

package verb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/variable"
	"github.com/bobmcallan/satellites/internal/workspace"
)

var variableStore *variable.Store

// SetVariableStore wires the server's variable.Store into the verb
// package. Called from cmd/satellites-server on boot.
func SetVariableStore(s *variable.Store) { variableStore = s }

// SystemVariableResolver returns a system-variable's computed value at
// request time. ok=false means the name isn't a known system variable.
type SystemVariableResolver func(ctx context.Context, name string) (value string, ok bool)

// SystemVariableLister returns every known system variable name. The
// list is the contract operators can read to discover what's available
// without having to keep a hardcoded copy.
type SystemVariableLister func(ctx context.Context) []string

var (
	sysResMu     sync.RWMutex
	sysResolver  SystemVariableResolver = func(context.Context, string) (string, bool) { return "", false }
	sysListNames SystemVariableLister   = func(context.Context) []string { return nil }
)

// SetSystemVariableResolver installs the computed-system-variable
// resolver. Story 5 plumbs the real per-request implementation; story
// 4's default is an empty resolver so workspace + project paths exercise
// alone.
func SetSystemVariableResolver(r SystemVariableResolver, l SystemVariableLister) {
	sysResMu.Lock()
	defer sysResMu.Unlock()
	if r == nil {
		r = func(context.Context, string) (string, bool) { return "", false }
	}
	if l == nil {
		l = func(context.Context) []string { return nil }
	}
	sysResolver = r
	sysListNames = l
}

func systemVariableResolve(ctx context.Context, name string) (string, bool) {
	sysResMu.RLock()
	defer sysResMu.RUnlock()
	return sysResolver(ctx, name)
}

func systemVariableNames(ctx context.Context) []string {
	sysResMu.RLock()
	defer sysResMu.RUnlock()
	return sysListNames(ctx)
}

// VariableGetRequest mirrors document_get's shape: name + scope +
// addressing columns + inherit cascade.
type VariableGetRequest struct {
	Name        string `json:"name"`
	Scope       string `json:"scope"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
	Inherit     bool   `json:"inherit,omitempty"`
}

// VariableGetResponse names the layer the value resolved at so callers
// can reason about precedence. For scope=system rows, value comes from
// the computed-variables resolver and ID/timestamps are empty.
type VariableGetResponse struct {
	Name          string `json:"name"`
	Value         string `json:"value"`
	ResolvedScope string `json:"resolved_scope"`
}

// VariableSetRequest is upsert-shaped: same value re-set is a no-op
// (apart from updated_at).
type VariableSetRequest struct {
	Name        string `json:"name"`
	Scope       string `json:"scope"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
	Value       string `json:"value"`
}

// VariableListRequest selects what to enumerate. Inherit=true folds in
// every layer up the chain (project → workspace → system), each row
// stamped with the layer it came from. Project precedence over
// workspace over system is applied so the returned set is what the
// caller would actually see after resolution.
type VariableListRequest struct {
	Scope       string `json:"scope"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
	Inherit     bool   `json:"inherit,omitempty"`
}

// VariableListEntry pairs a variable with the layer it resolved at.
type VariableListEntry struct {
	Name          string `json:"name"`
	Value         string `json:"value"`
	ResolvedScope string `json:"resolved_scope"`
}

// VariableListResponse is the merged listing.
type VariableListResponse struct {
	Variables []VariableListEntry `json:"variables"`
}

// VariableDeleteRequest is the delete payload.
type VariableDeleteRequest struct {
	Name        string `json:"name"`
	Scope       string `json:"scope"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
}

func init() {
	Register(&Verb{
		Name:        "variable_get",
		Description: "Fetch a variable by (scope, name); optional inherit cascade project→workspace→system.",
		Invoke:      invokeVariableGet,
	})
	Register(&Verb{
		Name:        "variable_set",
		Description: "Upsert a workspace/project-scoped variable's value.",
		Invoke:      invokeVariableSet,
	})
	Register(&Verb{
		Name:        "variable_list",
		Description: "List variables visible at the caller's scope, optionally folding in inherited layers.",
		Invoke:      invokeVariableList,
	})
	Register(&Verb{
		Name:        "variable_delete",
		Description: "Remove a workspace/project-scoped variable.",
		Invoke:      invokeVariableDelete,
	})
}

func invokeVariableGet(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if variableStore == nil {
		return nil, fmt.Errorf("variable_get: store not configured")
	}
	var req VariableGetRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("variable_get: %w: %v", ErrBadRequest, err)
		}
	}
	if strings.TrimSpace(req.Name) == "" {
		return nil, fmt.Errorf("variable_get: %w: name required", ErrBadRequest)
	}
	scope, err := parseVariableScope(req.Scope)
	if err != nil {
		return nil, err
	}
	for _, key := range buildVariableResolutionChain(req.Name, scope, req.WorkspaceID, req.ProjectID, req.Inherit) {
		if key.Scope == variable.ScopeSystem {
			// Stored-system rows take precedence over the computed
			// resolver. A seeded knob like stories.page_size lives in
			// the table; a computed name like version is never stored
			// and falls through to the resolver below.
			v, lookupErr := variableStore.Get(ctx, key)
			if lookupErr == nil {
				return json.Marshal(VariableGetResponse{Name: v.Name, Value: v.Value, ResolvedScope: "system"})
			}
			if !errors.Is(lookupErr, variable.ErrNotFound) {
				return nil, mapVariableStoreError(lookupErr, "variable_get")
			}
			if v, ok := systemVariableResolve(ctx, key.Name); ok {
				return json.Marshal(VariableGetResponse{Name: key.Name, Value: v, ResolvedScope: "system"})
			}
			continue
		}
		if err := authorizeVariableRead(ctx, key); err != nil {
			return nil, err
		}
		v, lookupErr := variableStore.Get(ctx, key)
		if lookupErr == nil {
			return json.Marshal(VariableGetResponse{Name: v.Name, Value: v.Value, ResolvedScope: string(v.Scope)})
		}
		if !errors.Is(lookupErr, variable.ErrNotFound) {
			return nil, mapVariableStoreError(lookupErr, "variable_get")
		}
	}
	return nil, fmt.Errorf("variable_get: %w: %s/%s", ErrNotFound, scope, req.Name)
}

func invokeVariableSet(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if variableStore == nil {
		return nil, fmt.Errorf("variable_set: store not configured")
	}
	var req VariableSetRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("variable_set: %w: %v", ErrBadRequest, err)
		}
	}
	if strings.TrimSpace(req.Name) == "" {
		return nil, fmt.Errorf("variable_set: %w: name required", ErrBadRequest)
	}
	scope, err := parseVariableScope(req.Scope)
	if err != nil {
		return nil, err
	}
	key := variable.Key{Scope: scope, WorkspaceID: req.WorkspaceID, ProjectID: req.ProjectID, Name: req.Name}
	if err := authorizeVariableWrite(ctx, key); err != nil {
		return nil, err
	}
	// Reject writes that target a name owned by the computed-system
	// resolver — those values are derived per request and can't be
	// persisted. Unknown system names ARE allowed; operators add new
	// knobs without code changes.
	if scope == variable.ScopeSystem {
		if _, isComputed := systemVariableResolve(ctx, req.Name); isComputed {
			return nil, fmt.Errorf("variable_set: %w: %q is a computed system variable (read-only)", ErrForbidden, req.Name)
		}
	}
	v, err := variableStore.Set(ctx, variable.SetInput{Key: key, Value: req.Value}, time.Now().UTC())
	if err != nil {
		return nil, mapVariableStoreError(err, "variable_set")
	}
	return json.Marshal(v)
}

func invokeVariableDelete(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if variableStore == nil {
		return nil, fmt.Errorf("variable_delete: store not configured")
	}
	var req VariableDeleteRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("variable_delete: %w: %v", ErrBadRequest, err)
		}
	}
	if strings.TrimSpace(req.Name) == "" {
		return nil, fmt.Errorf("variable_delete: %w: name required", ErrBadRequest)
	}
	scope, err := parseVariableScope(req.Scope)
	if err != nil {
		return nil, err
	}
	key := variable.Key{Scope: scope, WorkspaceID: req.WorkspaceID, ProjectID: req.ProjectID, Name: req.Name}
	if err := authorizeVariableWrite(ctx, key); err != nil {
		return nil, err
	}
	if err := variableStore.Delete(ctx, key); err != nil {
		return nil, mapVariableStoreError(err, "variable_delete")
	}
	return json.RawMessage(`{"deleted":true}`), nil
}

func invokeVariableList(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if variableStore == nil {
		return nil, fmt.Errorf("variable_list: store not configured")
	}
	var req VariableListRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("variable_list: %w: %v", ErrBadRequest, err)
		}
	}
	scope, err := parseVariableScope(req.Scope)
	if err != nil {
		return nil, err
	}
	// Authorize the deepest scope the caller asked about — that's the
	// most restrictive layer in the cascade and the one whose membership
	// matters. system listing alone needs no auth.
	deepest := variable.Key{Scope: scope, WorkspaceID: req.WorkspaceID, ProjectID: req.ProjectID, Name: "__list__"}
	if deepest.Scope != variable.ScopeSystem {
		if err := authorizeVariableRead(ctx, deepest); err != nil {
			return nil, err
		}
	}

	merged := map[string]VariableListEntry{}
	addRow := func(name, value string, layer variable.Scope) {
		// Higher-precedence layers come first in the cascade; don't let
		// a lower layer overwrite an existing entry.
		if _, exists := merged[name]; exists {
			return
		}
		merged[name] = VariableListEntry{Name: name, Value: value, ResolvedScope: string(layer)}
	}

	for _, layerKey := range buildVariableResolutionChain("", scope, req.WorkspaceID, req.ProjectID, req.Inherit) {
		switch layerKey.Scope {
		case variable.ScopeSystem:
			// Stored-system rows take precedence over computed names
			// (addRow is first-wins). A seeded knob like
			// stories.page_size surfaces here as scope='system'.
			vs, err := variableStore.ListByScope(ctx, variable.ScopeSystem, "", "")
			if err != nil {
				return nil, mapVariableStoreError(err, "variable_list")
			}
			for _, v := range vs {
				addRow(v.Name, v.Value, variable.ScopeSystem)
			}
			for _, name := range systemVariableNames(ctx) {
				if v, ok := systemVariableResolve(ctx, name); ok {
					addRow(name, v, variable.ScopeSystem)
				}
			}
		default:
			vs, err := variableStore.ListByScope(ctx, layerKey.Scope, layerKey.WorkspaceID, layerKey.ProjectID)
			if err != nil {
				return nil, mapVariableStoreError(err, "variable_list")
			}
			for _, v := range vs {
				addRow(v.Name, v.Value, v.Scope)
			}
		}
	}

	out := make([]VariableListEntry, 0, len(merged))
	for _, e := range merged {
		out = append(out, e)
	}
	sortVariableEntries(out)
	return json.Marshal(VariableListResponse{Variables: out})
}

func sortVariableEntries(out []VariableListEntry) {
	// Tiny insertion sort to keep the package's stdlib import set
	// unchanged; the merged map is small (operators set a handful of
	// vars per scope, not thousands).
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].Name > out[j].Name; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
}

func parseVariableScope(s string) (variable.Scope, error) {
	switch strings.TrimSpace(s) {
	case "system":
		return variable.ScopeSystem, nil
	case "workspace":
		return variable.ScopeWorkspace, nil
	case "project":
		return variable.ScopeProject, nil
	case "":
		return "", fmt.Errorf("variable: %w: scope required", ErrBadRequest)
	default:
		return "", fmt.Errorf("variable: %w: unknown scope %q", ErrBadRequest, s)
	}
}

// buildVariableResolutionChain mirrors the documents cascade, including
// the scope=system terminator. Empty name means "list at each layer";
// callers stamp the requested name otherwise.
func buildVariableResolutionChain(name string, scope variable.Scope, wsID, pjID string, inherit bool) []variable.Key {
	chain := []variable.Key{{Scope: scope, WorkspaceID: wsID, ProjectID: pjID, Name: name}}
	if !inherit {
		return chain
	}
	switch scope {
	case variable.ScopeProject:
		if wsID != "" {
			chain = append(chain, variable.Key{Scope: variable.ScopeWorkspace, WorkspaceID: wsID, Name: name})
		}
		chain = append(chain, variable.Key{Scope: variable.ScopeSystem, Name: name})
	case variable.ScopeWorkspace:
		chain = append(chain, variable.Key{Scope: variable.ScopeSystem, Name: name})
	}
	return chain
}

func authorizeVariableRead(ctx context.Context, key variable.Key) error {
	if authStore == nil {
		return nil
	}
	if key.Scope == variable.ScopeSystem {
		return nil
	}
	u := auth.FromContext(ctx)
	if u == nil {
		return fmt.Errorf("variable: %w: bearer required for %s scope", ErrUnauthorized, key.Scope)
	}
	if workspaceStore == nil {
		return nil
	}
	if key.WorkspaceID == "" {
		return fmt.Errorf("variable: %w: %s scope requires workspace_id", ErrBadRequest, key.Scope)
	}
	if _, err := workspaceStore.GetRole(ctx, key.WorkspaceID, u.ID); err != nil {
		if errors.Is(err, workspace.ErrMemberNotFound) {
			return fmt.Errorf("variable: %w: user not a member of workspace %s", ErrForbidden, key.WorkspaceID)
		}
		return err
	}
	return nil
}

func authorizeVariableWrite(ctx context.Context, key variable.Key) error {
	if key.Scope == variable.ScopeSystem {
		// Stored-system writes are allowed for any authenticated caller;
		// names owned by the computed resolver are rejected separately
		// inside invokeVariableSet so the error message can name the
		// specific name that's read-only.
		if authStore == nil {
			return nil
		}
		if u := auth.FromContext(ctx); u == nil {
			return fmt.Errorf("variable write: %w: bearer required for system scope", ErrUnauthorized)
		}
		return nil
	}
	return authorizeVariableRead(ctx, key)
}

func mapVariableStoreError(err error, prefix string) error {
	switch {
	case errors.Is(err, variable.ErrScopeReadonly):
		return fmt.Errorf("%s: %w: %v", prefix, ErrForbidden, err)
	case errors.Is(err, variable.ErrScopeMismatch):
		return fmt.Errorf("%s: %w: %v", prefix, ErrBadRequest, err)
	case errors.Is(err, variable.ErrNotFound):
		return fmt.Errorf("%s: %w: %v", prefix, ErrNotFound, err)
	default:
		return err
	}
}
