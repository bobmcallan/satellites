package portal

import (
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/ledger"
)

// adminKVData feeds the System KV admin page (story_6dc33b90).
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
	// Rows is the current system-tier KV listing, sorted by key.
	Rows []adminKVRow
	// ResolveKey is the user-supplied query parameter for the
	// resolved-view; empty when not set.
	ResolveKey string
	// Resolved is populated when ResolveKey is non-empty and the
	// resolver returned a hit. ResolvedFound distinguishes "no hit" from
	// "empty value at the system tier".
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

// handleAdminKV serves GET /admin/kv. Lists system-scope KV rows for
// global admins; non-admins receive 404 to avoid leaking page existence.
// story_6dc33b90.
func (p *Portal) handleAdminKV(w http.ResponseWriter, r *http.Request) {
	user, ok := p.resolveUser(r)
	if !ok {
		p.redirectToLogin(w, r)
		return
	}
	if !p.isGlobalAdmin(user) {
		http.NotFound(w, r)
		return
	}
	active, chips, memberships := p.activeWorkspace(r, user)
	rows := p.loadSystemKVRows(r)

	resolveKey := r.URL.Query().Get("resolve")
	var resolved adminKVRow
	resolvedFound := false
	if resolveKey != "" && p.ledger != nil {
		// Resolved-read uses caller's full context: workspace from the
		// active selector, user_id from the session. Project is omitted
		// here because the admin page sits outside any specific project.
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
		Title:           buildPageTitle(active, "", "system kv"),
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

// loadSystemKVRows returns the system-scope KV rows sorted by key for
// the admin browser. Returns an empty slice when no rows exist or the
// ledger isn't wired.
func (p *Portal) loadSystemKVRows(r *http.Request) []adminKVRow {
	if p.ledger == nil {
		return nil
	}
	// System rows live with WorkspaceID="" — pass [""] memberships so
	// the system rows pass the membership filter.
	rows, err := ledger.KVProjectionScoped(r.Context(), p.ledger, ledger.KVProjectionOptions{
		Scope: ledger.KVScopeSystem,
	}, []string{""})
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

// handleAdminKVSet writes a system-scope KV row. Form-encoded POST of
// `key` and `value`. Global-admin only; non-admins → 404.
func (p *Portal) handleAdminKVSet(w http.ResponseWriter, r *http.Request) {
	user, ok := p.resolveUser(r)
	if !ok {
		p.redirectToLogin(w, r)
		return
	}
	if !p.isGlobalAdmin(user) {
		http.NotFound(w, r)
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
	key := r.PostFormValue("key")
	value := r.PostFormValue("value")
	if key == "" {
		p.redirectAdminKV(w, r, "key required", "error")
		return
	}
	if p.ledger == nil {
		p.redirectAdminKV(w, r, "ledger unavailable", "error")
		return
	}
	entry := ledger.LedgerEntry{
		Type:      ledger.TypeKV,
		Tags:      []string{"scope:" + string(ledger.KVScopeSystem), "key:" + key},
		Content:   value,
		CreatedBy: user.ID,
	}
	if _, err := p.ledger.Append(r.Context(), entry, time.Now().UTC()); err != nil {
		p.logger.Warn().Str("error", err.Error()).Msg("admin kv set: append failed")
		p.redirectAdminKV(w, r, "set failed: "+err.Error(), "error")
		return
	}
	p.redirectAdminKV(w, r, "set "+key, "ok")
}

// handleAdminKVDelete appends a system-scope tombstone row. Global-admin only.
func (p *Portal) handleAdminKVDelete(w http.ResponseWriter, r *http.Request) {
	user, ok := p.resolveUser(r)
	if !ok {
		p.redirectToLogin(w, r)
		return
	}
	if !p.isGlobalAdmin(user) {
		http.NotFound(w, r)
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
	key := r.PostFormValue("key")
	if key == "" {
		p.redirectAdminKV(w, r, "key required", "error")
		return
	}
	if p.ledger == nil {
		p.redirectAdminKV(w, r, "ledger unavailable", "error")
		return
	}
	entry := ledger.LedgerEntry{
		Type:      ledger.TypeKV,
		Tags:      []string{"scope:" + string(ledger.KVScopeSystem), "key:" + key, ledger.KVTombstoneTag},
		Content:   "",
		CreatedBy: user.ID,
	}
	if _, err := p.ledger.Append(r.Context(), entry, time.Now().UTC()); err != nil {
		p.logger.Warn().Str("error", err.Error()).Msg("admin kv delete: append failed")
		p.redirectAdminKV(w, r, "delete failed: "+err.Error(), "error")
		return
	}
	p.redirectAdminKV(w, r, "deleted "+key, "ok")
}

// redirectAdminKV redirects back to /admin/kv with a flash payload.
func (p *Portal) redirectAdminKV(w http.ResponseWriter, r *http.Request, msg, kind string) {
	q := url.Values{}
	q.Set("flash", msg)
	q.Set("flash_kind", kind)
	http.Redirect(w, r, "/admin/kv?"+q.Encode(), http.StatusSeeOther)
}
