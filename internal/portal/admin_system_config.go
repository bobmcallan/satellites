package portal

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/configseed"
	"github.com/bobmcallan/satellites/internal/ledger"
)

// adminSystemConfigData feeds the System Config admin page. Only
// global_admin sessions ever see this; non-admins get a 404 from the
// handler. story_33e1a323.
type adminSystemConfigData struct {
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
	LastRun         *systemSeedRunRow
	SeedDir         string
	HelpDir         string
}

// systemSeedRunRow is the view-model for the latest kind:system-seed-run
// ledger entry surfaced on the admin page.
type systemSeedRunRow struct {
	LedgerID  string
	CreatedAt string
	CreatedBy string
	Loaded    int
	Created   int
	Updated   int
	Skipped   int
	Errors    []configseed.ErrorEntry
}

// isGlobalAdmin reports whether the current user is permitted to view
// admin-tier surfaces. Mirrors the auth.IsGlobalAdmin helper but reads
// the env list cached on Portal.globalAdminEmails (story_3548cde2).
func (p *Portal) isGlobalAdmin(user auth.User) bool {
	return auth.IsGlobalAdmin(user, p.globalAdminEmails)
}

// loadLatestSeedRun fetches the most recent kind:system-seed-run row
// from the ledger and projects it for the template. Returns nil when
// no run has happened yet or the ledger is unwired.
func (p *Portal) loadLatestSeedRun(ctx context.Context) *systemSeedRunRow {
	if p.ledger == nil {
		return nil
	}
	rows, err := p.ledger.List(ctx, "", ledger.ListOptions{
		Type: ledger.TypeDecision,
		Tags: []string{"kind:system-seed-run"},
	}, nil)
	if err != nil || len(rows) == 0 {
		return nil
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].CreatedAt.After(rows[j].CreatedAt) })
	latest := rows[0]
	out := &systemSeedRunRow{
		LedgerID:  latest.ID,
		CreatedAt: latest.CreatedAt.UTC().Format(time.RFC3339),
		CreatedBy: latest.CreatedBy,
	}
	if len(latest.Structured) > 0 {
		var payload struct {
			Loaded  int                     `json:"loaded"`
			Created int                     `json:"created"`
			Updated int                     `json:"updated"`
			Skipped int                     `json:"skipped"`
			Errors  []configseed.ErrorEntry `json:"errors"`
		}
		if err := json.Unmarshal(latest.Structured, &payload); err == nil {
			out.Loaded = payload.Loaded
			out.Created = payload.Created
			out.Updated = payload.Updated
			out.Skipped = payload.Skipped
			out.Errors = payload.Errors
		}
	}
	return out
}

// handleAdminSystemConfig renders the System Config admin page.
// Returns 404 for non-admins to avoid leaking the page's existence.
func (p *Portal) handleAdminSystemConfig(w http.ResponseWriter, r *http.Request) {
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
	data := adminSystemConfigData{
		Title:           buildPageTitle(active, "", "system config"),
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
		LastRun:         p.loadLatestSeedRun(r.Context()),
		SeedDir:         configseed.ResolveSeedDir(),
		HelpDir:         configseed.ResolveHelpDir(),
	}
	if err := p.tmpl.ExecuteTemplate(w, "admin_system_config.html", data); err != nil {
		p.logger.Error().Str("template", "admin_system_config.html").Str("error", err.Error()).Msg("template render failed")
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

// handleAdminSystemConfigReseed runs configseed.RunAll, writes the
// kind:system-seed-run ledger row, then redirects back to the admin
// page so the operator sees the new summary. Non-admins → 404.
func (p *Portal) handleAdminSystemConfigReseed(w http.ResponseWriter, r *http.Request) {
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

	now := time.Now().UTC()
	workspaceID := p.systemWorkspaceID(r.Context())
	summary, err := configseed.RunAll(r.Context(), p.documents,
		configseed.ResolveSeedDir(), configseed.ResolveHelpDir(),
		workspaceID, user.ID, now)
	if err != nil {
		p.logger.Error().Str("error", err.Error()).Msg("system seed reseed failed")
	}
	if p.ledger != nil {
		structured, _ := json.Marshal(struct {
			Loaded  int                     `json:"loaded"`
			Created int                     `json:"created"`
			Updated int                     `json:"updated"`
			Skipped int                     `json:"skipped"`
			Errors  []configseed.ErrorEntry `json:"errors,omitempty"`
		}{summary.Loaded, summary.Created, summary.Updated, summary.Skipped, summary.Errors})
		row := ledger.LedgerEntry{
			WorkspaceID: workspaceID,
			Type:        ledger.TypeDecision,
			Tags:        []string{"kind:system-seed-run"},
			Content:     "system seed run via portal",
			Structured:  structured,
			Durability:  ledger.DurabilityDurable,
			SourceType:  ledger.SourceUser,
			CreatedBy:   user.ID,
		}
		if _, lerr := p.ledger.Append(r.Context(), row, now); lerr != nil {
			p.logger.Warn().Str("error", lerr.Error()).Msg("system-seed-run ledger append failed")
		}
	}
	http.Redirect(w, r, "/admin/system-config", http.StatusSeeOther)
}

// systemWorkspaceID resolves the system workspace via its first
// member (the bootstrap "system" / "apikey" user) or the active
// session's first workspace. Empty when no resolver succeeds.
func (p *Portal) systemWorkspaceID(ctx context.Context) string {
	if p.workspaces == nil {
		return ""
	}
	if list, err := p.workspaces.ListByMember(ctx, "system"); err == nil && len(list) > 0 {
		return list[0].ID
	}
	if list, err := p.workspaces.ListByMember(ctx, "apikey"); err == nil && len(list) > 0 {
		return list[0].ID
	}
	return ""
}
