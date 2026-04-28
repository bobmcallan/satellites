package portal

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"sort"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/document"
)

// helpEntry is the per-row view-model for the help index. story_42f2f2c0.
type helpEntry struct {
	Slug  string
	Title string
	Order int
}

type helpIndexData struct {
	Title           string
	Version         string
	Commit          string
	User            auth.User
	Entries         []helpEntry
	Workspaces      []wsChip
	ActiveWorkspace wsChip
	DevMode         bool
	GlobalAdminChip bool
	ThemeMode       string
	ThemePickerNext string
	WSConfig        WSConfig
}

type helpDetailData struct {
	Title           string
	Version         string
	Commit          string
	User            auth.User
	Entry           helpEntry
	BodyHTML        template.HTML
	Workspaces      []wsChip
	ActiveWorkspace wsChip
	DevMode         bool
	GlobalAdminChip bool
	ThemeMode       string
	ThemePickerNext string
	WSConfig        WSConfig
}

// loadHelpEntries lists all (scope=system, type=help) docs and sorts
// them by Structured.order ascending, then by slug. Empty slice when
// the document store is absent.
func (p *Portal) loadHelpEntries(ctx context.Context) []helpEntry {
	if p.documents == nil {
		return nil
	}
	rows, err := p.documents.List(ctx, document.ListOptions{
		Type:  document.TypeHelp,
		Scope: document.ScopeSystem,
		Limit: 200,
	}, nil)
	if err != nil {
		p.logger.Warn().Str("error", err.Error()).Msg("help list failed")
		return nil
	}
	out := make([]helpEntry, 0, len(rows))
	for _, d := range rows {
		out = append(out, helpEntryFor(d))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].Slug < out[j].Slug
	})
	return out
}

// helpEntryFor pulls title/order from the structured payload that
// configseed.helpToInput wrote.
func helpEntryFor(d document.Document) helpEntry {
	entry := helpEntry{Slug: d.Name, Title: d.Name}
	if len(d.Structured) == 0 {
		return entry
	}
	var payload map[string]any
	if err := json.Unmarshal(d.Structured, &payload); err != nil {
		return entry
	}
	if title, ok := payload["title"].(string); ok && title != "" {
		entry.Title = title
	}
	if slug, ok := payload["slug"].(string); ok && slug != "" {
		entry.Slug = slug
	}
	switch order := payload["order"].(type) {
	case int:
		entry.Order = order
	case int64:
		entry.Order = int(order)
	case float64:
		entry.Order = int(order)
	}
	return entry
}

// handleHelpIndex renders the help page list.
func (p *Portal) handleHelpIndex(w http.ResponseWriter, r *http.Request) {
	user, ok := p.resolveUser(r)
	if !ok {
		p.redirectToLogin(w, r)
		return
	}
	active, chips, memberships := p.activeWorkspace(r, user)
	entries := p.loadHelpEntries(r.Context())

	data := helpIndexData{
		Title:           buildPageTitle(active, "", "help"),
		Version:         config.Version,
		Commit:          config.GitCommit,
		User:            user,
		Entries:         entries,
		Workspaces:      chips,
		ActiveWorkspace: active,
		DevMode:         p.cfg.Env != "prod" && p.cfg.DevMode,
		GlobalAdminChip: p.globalAdminChip(user, active, memberships),
		ThemeMode:       themeFromRequest(r),
		ThemePickerNext: r.URL.RequestURI(),
		WSConfig:        buildWSConfig(active, r),
	}
	if err := p.tmpl.ExecuteTemplate(w, "help_index.html", data); err != nil {
		p.logger.Error().Str("template", "help_index.html").Str("error", err.Error()).Msg("template render failed")
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

// handleHelpDetail renders one help page by slug.
func (p *Portal) handleHelpDetail(w http.ResponseWriter, r *http.Request) {
	user, ok := p.resolveUser(r)
	if !ok {
		p.redirectToLogin(w, r)
		return
	}
	if p.documents == nil {
		http.NotFound(w, r)
		return
	}
	slug := r.PathValue("slug")
	if slug == "" {
		http.NotFound(w, r)
		return
	}
	doc, err := p.documents.GetByName(r.Context(), "", slug, nil)
	if err != nil || doc.Type != document.TypeHelp || doc.Scope != document.ScopeSystem {
		http.NotFound(w, r)
		return
	}

	active, chips, memberships := p.activeWorkspace(r, user)
	data := helpDetailData{
		Title:           buildPageTitle(active, "", "help · "+slug),
		Version:         config.Version,
		Commit:          config.GitCommit,
		User:            user,
		Entry:           helpEntryFor(doc),
		BodyHTML:        RenderHelpMarkdown(doc.Body),
		Workspaces:      chips,
		ActiveWorkspace: active,
		DevMode:         p.cfg.Env != "prod" && p.cfg.DevMode,
		GlobalAdminChip: p.globalAdminChip(user, active, memberships),
		ThemeMode:       themeFromRequest(r),
		ThemePickerNext: r.URL.RequestURI(),
		WSConfig:        buildWSConfig(active, r),
	}
	if err := p.tmpl.ExecuteTemplate(w, "help_detail.html", data); err != nil {
		p.logger.Error().Str("template", "help_detail.html").Str("error", err.Error()).Msg("template render failed")
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}
