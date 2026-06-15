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

// reservedSecretNames are system-scope names that hold LLM credentials and
// MUST be written as secrets — a plain (non-secret) variable cannot hold a
// key. The server-internal LLM-config resolver reads them directly; the
// public read verbs redact them. Keep in sync with the resolver's env
// fallbacks in cmd/satellites-server.
var reservedSecretNames = map[string]bool{
	"gemini.api_key":    true,
	"anthropic.api_key": true,
}

// redactedSecretValue replaces a secret's plaintext on every public read
// path. The resolver bypasses the verbs and reads the store directly.
const redactedSecretValue = ""

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
	Secret        bool   `json:"secret,omitempty"`
}

// VariableSetRequest is upsert-shaped: same value re-set is a no-op
// (apart from updated_at).
type VariableSetRequest struct {
	Name        string `json:"name"`
	Scope       string `json:"scope"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
	Value       string `json:"value"`
	// Secret marks the row as a credential: admin-gated on write, never
	// echoed by variable_get / variable_list. Reserved key names (see
	// reservedSecretNames) MUST be written with secret:true.
	Secret bool `json:"secret,omitempty"`
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

// VariableListEntry pairs a variable with the layer it resolved at. A
// secret entry carries its name + secret:true but never its value.
type VariableListEntry struct {
	Name          string `json:"name"`
	Value         string `json:"value"`
	ResolvedScope string `json:"resolved_scope"`
	Secret        bool   `json:"secret,omitempty"`
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
	for _, key := range buildVariableResolutionChain(req.Name, scope, req.WorkspaceID, req.ProjectID, callerUserID(ctx), req.Inherit) {
		if key.Scope == variable.ScopeSystem {
			// Stored-system rows take precedence over the computed
			// resolver. A seeded knob like stories.page_size lives in
			// the table; a computed name like version is never stored
			// and falls through to the resolver below.
			v, lookupErr := variableStore.Get(ctx, key)
			if lookupErr == nil {
				return json.Marshal(variableGetResponse(v, "system"))
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
			return json.Marshal(variableGetResponse(v, string(v.Scope)))
		}
		if !errors.Is(lookupErr, variable.ErrNotFound) {
			return nil, mapVariableStoreError(lookupErr, "variable_get")
		}
	}
	return nil, fmt.Errorf("variable_get: %w: %s/%s", ErrNotFound, scope, req.Name)
}

// variableGetResponse builds the read response, blanking a secret's value
// so its plaintext is never returned by variable_get.
func variableGetResponse(v variable.Variable, resolvedScope string) VariableGetResponse {
	if v.Secret {
		return VariableGetResponse{Name: v.Name, Value: redactedSecretValue, ResolvedScope: resolvedScope, Secret: true}
	}
	return VariableGetResponse{Name: v.Name, Value: v.Value, ResolvedScope: resolvedScope}
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
	// A user-scope write is keyed to the caller's own identity — never an
	// arbitrary user_id from the request.
	if scope == variable.ScopeUser {
		key.UserID = callerUserID(ctx)
		if key.UserID == "" {
			return nil, fmt.Errorf("variable_set: %w: user scope requires a caller identity", ErrBadRequest)
		}
	}
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
	// A reserved key name (gemini.api_key, …) is a credential — it may
	// only be written as a secret. A plain variable cannot hold a key.
	if reservedSecretNames[req.Name] && !req.Secret {
		return nil, fmt.Errorf("variable_set: %w: %q is a reserved credential name and must be written with secret:true", ErrBadRequest, req.Name)
	}
	// Secret writes are admin-gated (global admin). CLI-local dispatch
	// without an auth store is exempt, like the other authz call sites.
	if req.Secret {
		if err := requireSecretWriteAdmin(ctx); err != nil {
			return nil, err
		}
	}
	v, err := variableStore.Set(ctx, variable.SetInput{Key: key, Value: req.Value, Secret: req.Secret}, time.Now().UTC())
	if err != nil {
		return nil, mapVariableStoreError(err, "variable_set")
	}
	// Never echo the stored plaintext back on write either.
	if v.Secret {
		v.Value = redactedSecretValue
	}
	return json.Marshal(v)
}

// requireSecretWriteAdmin enforces that secret variable writes come from a
// global admin. CLI-local in-process dispatch (no auth store wired) is
// exempt, consistent with the rest of the authz surface.
func requireSecretWriteAdmin(ctx context.Context) error {
	if authStore == nil {
		return nil
	}
	u := auth.FromContext(ctx)
	if u == nil {
		return fmt.Errorf("variable_set: %w: bearer required to write a secret", ErrUnauthorized)
	}
	if u.Role != auth.RoleAdmin {
		return fmt.Errorf("variable_set: %w: secret writes require a global admin", ErrForbidden)
	}
	return nil
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
	if scope == variable.ScopeUser {
		key.UserID = callerUserID(ctx)
		if key.UserID == "" {
			return nil, fmt.Errorf("variable_delete: %w: user scope requires a caller identity", ErrBadRequest)
		}
	}
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
	if scope == variable.ScopeUser {
		deepest.UserID = callerUserID(ctx)
	}
	if deepest.Scope != variable.ScopeSystem {
		if err := authorizeVariableRead(ctx, deepest); err != nil {
			return nil, err
		}
	}

	merged := map[string]VariableListEntry{}
	addRow := func(name, value string, layer variable.Scope, secret bool) {
		// Higher-precedence layers come first in the cascade; don't let
		// a lower layer overwrite an existing entry.
		if _, exists := merged[name]; exists {
			return
		}
		// A secret's plaintext is never listed — only its name + marker.
		if secret {
			value = redactedSecretValue
		}
		merged[name] = VariableListEntry{Name: name, Value: value, ResolvedScope: string(layer), Secret: secret}
	}

	for _, layerKey := range buildVariableResolutionChain("", scope, req.WorkspaceID, req.ProjectID, callerUserID(ctx), req.Inherit) {
		switch layerKey.Scope {
		case variable.ScopeUser:
			// The caller's user-override layer keys on user_id, not the
			// workspace/project columns ListByScope expects.
			if layerKey.UserID == "" {
				continue
			}
			vs, err := variableStore.ListByUser(ctx, layerKey.UserID)
			if err != nil {
				return nil, mapVariableStoreError(err, "variable_list")
			}
			for _, v := range vs {
				addRow(v.Name, v.Value, variable.ScopeUser, v.Secret)
			}
		case variable.ScopeSystem:
			// Stored-system rows take precedence over computed names
			// (addRow is first-wins). A seeded knob like
			// stories.page_size surfaces here as scope='system'.
			vs, err := variableStore.ListByScope(ctx, variable.ScopeSystem, "", "")
			if err != nil {
				return nil, mapVariableStoreError(err, "variable_list")
			}
			for _, v := range vs {
				addRow(v.Name, v.Value, variable.ScopeSystem, v.Secret)
			}
			for _, name := range systemVariableNames(ctx) {
				if v, ok := systemVariableResolve(ctx, name); ok {
					addRow(name, v, variable.ScopeSystem, false)
				}
			}
		default:
			vs, err := variableStore.ListByScope(ctx, layerKey.Scope, layerKey.WorkspaceID, layerKey.ProjectID)
			if err != nil {
				return nil, mapVariableStoreError(err, "variable_list")
			}
			for _, v := range vs {
				addRow(v.Name, v.Value, v.Scope, v.Secret)
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
	case "user":
		return variable.ScopeUser, nil
	case "":
		return "", fmt.Errorf("variable: %w: scope required", ErrBadRequest)
	default:
		return "", fmt.Errorf("variable: %w: unknown scope %q", ErrBadRequest, s)
	}
}

// buildVariableResolutionChain mirrors the documents cascade, including
// the scope=system terminator and the highest-precedence user override
// layer (sty_6cdf1cd0). Empty name means "list at each layer"; callers
// stamp the requested name otherwise. userID is the caller's identity: a
// non-user inherited read prepends the caller's user layer at the top of
// the cascade (user → project → workspace → system); a scope=user read
// resolves the single user key.
func buildVariableResolutionChain(name string, scope variable.Scope, wsID, pjID, userID string, inherit bool) []variable.Key {
	base := variable.Key{Scope: scope, WorkspaceID: wsID, ProjectID: pjID, Name: name}
	if scope == variable.ScopeUser {
		base.UserID = userID
	}
	chain := []variable.Key{base}
	if !inherit {
		return chain
	}
	// Prepend the caller's user override layer at highest precedence for
	// non-user reads.
	if userID != "" && scope != variable.ScopeUser {
		chain = append([]variable.Key{{Scope: variable.ScopeUser, UserID: userID, Name: name}}, chain...)
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
	// A user-scope row is readable only by its owner — never another caller.
	if key.Scope == variable.ScopeUser {
		if key.UserID != u.ID {
			return fmt.Errorf("variable: %w: user scope is readable only by its owner", ErrForbidden)
		}
		return nil
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
