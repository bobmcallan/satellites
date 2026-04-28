package portal

import (
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/workspace"
)

// adminKVData feeds the KV admin page (story_6dc33b90). Reads are open
// to any authenticated workspace member; writes are role-gated per
// scope by canWriteScope.
type adminKVData struct {
	Title           string
	Version         string
	Commit          string
	User            auth.User
	Workspaces      []wsChip
	ActiveWorkspace wsChip
	DevMode         bool
	GlobalAdminChip bool
	IsGlobalAdmin   bool
	ThemeMode       string
	ThemePickerNext string
	WSConfig        WSConfig
	// Scope is the selected scope tab — system | workspace | project |
	// user. Defaults to system when no `scope` query param is supplied.
	Scope string
	// Scopes lists the visible tabs in render order.
	Scopes []string
	// CanWrite reports whether the current caller may set/delete at
	// the selected Scope. Templates use this to gate the set/delete UI.
	CanWrite bool
	// Rows is the current scope's KV listing, sorted by key.
	Rows []adminKVRow
	// ResolveKey is the user-supplied query parameter for the
	// resolved-view; empty when not set.
	ResolveKey string
	// Resolved is populated when ResolveKey is non-empty and the
	// resolver returned a hit. ResolvedFound distinguishes "no hit" from
	// "empty value at any tier".
	Resolved      adminKVRow
	ResolvedFound bool
	// Flash carries a transient success/error message from the most
	// recent POST. Re-rendered on the next GET via query string.
	Flash     string
	FlashKind string // "ok" | "error"
}

// adminKVRow is the per-row view-model. Mirrors the ledger.KVRow shape
// flattened for templates.
type adminKVRow struct {
	Key       string
	Value     string
	Scope     string
	UpdatedAt string
	UpdatedBy string
	EntryID   string
}

// resolveAdminKVScope normalises the `scope` query parameter against
// the four supported KV scopes. Defaults to system when absent or
// invalid.
func resolveAdminKVScope(raw string) ledger.KVScope {
	switch ledger.KVScope(raw) {
	case ledger.KVScopeSystem, ledger.KVScopeWorkspace, ledger.KVScopeProject, ledger.KVScopeUser:
		return ledger.KVScope(raw)
	default:
		return ledger.KVScopeSystem
	}
}

// canWriteAdminKVScope mirrors the per-scope role gate from
// kvCheckWriteAuth in mcpserver/kv_handlers.go (story_eb17cb16).
// Implemented locally so the portal page can flip UI affordances
// without round-tripping through MCP.
func (p *Portal) canWriteAdminKVScope(r *http.Request, scope ledger.KVScope, user auth.User, active wsChip) bool {
	switch scope {
	case ledger.KVScopeSystem:
		return p.isGlobalAdmin(user)
	case ledger.KVScopeWorkspace:
		if p.isGlobalAdmin(user) {
			return true
		}
		if p.workspaces == nil || active.ID == "" {
			return false
		}
		role, err := p.workspaces.GetRole(r.Context(), active.ID, user.ID)
		return err == nil && role == workspace.RoleAdmin
	case ledger.KVScopeProject:
		// Project scope writes from the admin page need a project
		// context; the v1 admin page sits outside any specific project,
		// so writes are gated via global-admin override only.
		return p.isGlobalAdmin(user)
	case ledger.KVScopeUser:
		// Any authenticated user may write to their own user-scope KV.
		return user.ID != ""
	}
	return false
}

// handleAdminKV serves GET /admin/kv. Open to any authenticated user
// (story_6dc33b90); the per-scope tabs render rows the caller is
// allowed to read, and the set/delete forms only render for scopes
// where the caller passes canWriteAdminKVScope.
func (p *Portal) handleAdminKV(w http.ResponseWriter, r *http.Request) {
	user, ok := p.resolveUser(r)
	if !ok {
		p.redirectToLogin(w, r)
		return
	}
	active, chips, memberships := p.activeWorkspace(r, user)
	scope := resolveAdminKVScope(r.URL.Query().Get("scope"))
	rows := p.loadKVRowsForScope(r, scope, user, active, memberships)

	resolveKey := r.URL.Query().Get("resolve")
	var resolved adminKVRow
	resolvedFound := false
	if resolveKey != "" && p.ledger != nil {
		opts := ledger.KVResolveOptions{
			WorkspaceID: active.ID,
			UserID:      user.ID,
		}
		row, found, _ := ledger.KVResolveScoped(r.Context(), p.ledger, resolveKey, opts, append([]string{""}, memberships...))
		if found {
			resolved = adminKVRow{
				Key:       row.Key,
				Value:     row.Value,
				Scope:     string(row.Scope),
				UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339),
				UpdatedBy: row.UpdatedBy,
				EntryID:   row.EntryID,
			}
			resolvedFound = true
		}
	}

	flash := r.URL.Query().Get("flash")
	flashKind := r.URL.Query().Get("flash_kind")

	data := adminKVData{
		Title:           buildPageTitle(active, "", "kv"),
		Version:         config.Version,
		Commit:          config.GitCommit,
		User:            user,
		Workspaces:      chips,
		ActiveWorkspace: active,
		DevMode:         p.cfg.Env != "prod" && p.cfg.DevMode,
		GlobalAdminChip: p.globalAdminChip(user, active, memberships),
		IsGlobalAdmin:   p.isGlobalAdmin(user),
		ThemeMode:       themeFromRequest(r),
		ThemePickerNext: r.URL.RequestURI(),
		WSConfig:        buildWSConfig(active, r),
		Scope:           string(scope),
		Scopes:          []string{string(ledger.KVScopeSystem), string(ledger.KVScopeWorkspace), string(ledger.KVScopeProject), string(ledger.KVScopeUser)},
		CanWrite:        p.canWriteAdminKVScope(r, scope, user, active),
		Rows:            rows,
		ResolveKey:      resolveKey,
		Resolved:        resolved,
		ResolvedFound:   resolvedFound,
		Flash:           flash,
		FlashKind:       flashKind,
	}
	if err := p.tmpl.ExecuteTemplate(w, "admin_kv.html", data); err != nil {
		p.logger.Error().Str("template", "admin_kv.html").Str("error", err.Error()).Msg("template render failed")
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

// loadKVRowsForScope projects the ledger for the requested scope using
// the caller's active workspace + user_id. Scopes the caller can't
// read return an empty slice.
func (p *Portal) loadKVRowsForScope(r *http.Request, scope ledger.KVScope, user auth.User, active wsChip, memberships []string) []adminKVRow {
	if p.ledger == nil {
		return nil
	}
	opts := ledger.KVProjectionOptions{Scope: scope}
	listMemberships := memberships
	switch scope {
	case ledger.KVScopeSystem:
		listMemberships = []string{""}
	case ledger.KVScopeWorkspace:
		opts.WorkspaceID = active.ID
		if active.ID == "" {
			return nil
		}
	case ledger.KVScopeProject:
		// v1: project-scope browse from the admin page needs a project
		// context the page doesn't carry yet. Return empty until the
		// project scope tab gets a project selector (deferred).
		return nil
	case ledger.KVScopeUser:
		opts.WorkspaceID = active.ID
		opts.UserID = user.ID
		if user.ID == "" || active.ID == "" {
			return nil
		}
	}
	rows, err := ledger.KVProjectionScoped(r.Context(), p.ledger, opts, listMemberships)
	if err != nil {
		p.logger.Warn().Str("error", err.Error()).Msg("admin kv: projection failed")
		return nil
	}
	out := make([]adminKVRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, adminKVRow{
			Key:       row.Key,
			Value:     row.Value,
			Scope:     string(row.Scope),
			UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339),
			UpdatedBy: row.UpdatedBy,
			EntryID:   row.EntryID,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// adminKVWriteEntry constructs the LedgerEntry for a portal-side write.
// Mirrors mcpserver/kvWriteEntry but lives in the portal package to
// keep the dependency direction one-way.
func adminKVWriteEntry(scope ledger.KVScope, active wsChip, user auth.User, key, value string, tombstone bool) ledger.LedgerEntry {
	tags := []string{"scope:" + string(scope), "key:" + key}
	entry := ledger.LedgerEntry{
		Type:      ledger.TypeKV,
		Tags:      tags,
		Content:   value,
		CreatedBy: user.ID,
	}
	switch scope {
	case ledger.KVScopeWorkspace:
		entry.WorkspaceID = active.ID
	case ledger.KVScopeUser:
		entry.WorkspaceID = active.ID
		entry.Tags = append(entry.Tags, "user:"+user.ID)
	}
	if tombstone {
		entry.Tags = append(entry.Tags, ledger.KVTombstoneTag)
	}
	return entry
}

// handleAdminKVSet writes a KV row at the requested scope. Form-
// encoded POST: `scope`, `key`, `value`. Per-scope role gating mirrors
// mcpserver/kvCheckWriteAuth (story_eb17cb16).
func (p *Portal) handleAdminKVSet(w http.ResponseWriter, r *http.Request) {
	user, ok := p.resolveUser(r)
	if !ok {
		p.redirectToLogin(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	active, _, _ := p.activeWorkspace(r, user)
	scope := resolveAdminKVScope(r.PostFormValue("scope"))
	if !p.canWriteAdminKVScope(r, scope, user, active) {
		p.redirectAdminKV(w, r, "forbidden: scope="+string(scope), "error", scope)
		return
	}
	key := r.PostFormValue("key")
	value := r.PostFormValue("value")
	if key == "" {
		p.redirectAdminKV(w, r, "key required", "error", scope)
		return
	}
	if p.ledger == nil {
		p.redirectAdminKV(w, r, "ledger unavailable", "error", scope)
		return
	}
	entry := adminKVWriteEntry(scope, active, user, key, value, false)
	if _, err := p.ledger.Append(r.Context(), entry, time.Now().UTC()); err != nil {
		p.logger.Warn().Str("error", err.Error()).Msg("admin kv set: append failed")
		p.redirectAdminKV(w, r, "set failed: "+err.Error(), "error", scope)
		return
	}
	p.redirectAdminKV(w, r, "set "+key, "ok", scope)
}

// handleAdminKVDelete appends a tombstone row at the requested scope.
func (p *Portal) handleAdminKVDelete(w http.ResponseWriter, r *http.Request) {
	user, ok := p.resolveUser(r)
	if !ok {
		p.redirectToLogin(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	active, _, _ := p.activeWorkspace(r, user)
	scope := resolveAdminKVScope(r.PostFormValue("scope"))
	if !p.canWriteAdminKVScope(r, scope, user, active) {
		p.redirectAdminKV(w, r, "forbidden: scope="+string(scope), "error", scope)
		return
	}
	key := r.PostFormValue("key")
	if key == "" {
		p.redirectAdminKV(w, r, "key required", "error", scope)
		return
	}
	if p.ledger == nil {
		p.redirectAdminKV(w, r, "ledger unavailable", "error", scope)
		return
	}
	entry := adminKVWriteEntry(scope, active, user, key, "", true)
	if _, err := p.ledger.Append(r.Context(), entry, time.Now().UTC()); err != nil {
		p.logger.Warn().Str("error", err.Error()).Msg("admin kv delete: append failed")
		p.redirectAdminKV(w, r, "delete failed: "+err.Error(), "error", scope)
		return
	}
	p.redirectAdminKV(w, r, "deleted "+key, "ok", scope)
}

// redirectAdminKV redirects back to /admin/kv preserving the active
// scope tab and carrying a flash payload.
func (p *Portal) redirectAdminKV(w http.ResponseWriter, r *http.Request, msg, kind string, scope ledger.KVScope) {
	q := url.Values{}
	q.Set("scope", string(scope))
	q.Set("flash", msg)
	q.Set("flash_kind", kind)
	http.Redirect(w, r, "/admin/kv?"+q.Encode(), http.StatusSeeOther)
}
