package server

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/verb"
)

var projectsTmpl = template.Must(template.ParseFS(assets, "templates/projects.html", "templates/_user_menu.html"))

type projectsData struct {
	Title       string
	UserEmail   string
	UserName    string
	UserAvatar  string
	ActiveNav   string
	Projects    []projectRow
	Types       []projectTypeChip
	ActiveType  string
	FlashError  string
	DevMode     bool
	FooterName  string
	FooterEmail string
	Version     string
}

// projectTypeChip is one entry in the projects-page type-filter strip: a `type`
// value present in the data, flagged active when it is the selected filter.
type projectTypeChip struct {
	Type   string
	Active bool
}

type projectRow struct {
	ID          string
	Name        string
	Description string
	Type        string
	Phase       string
	GitURL      string
	Status      string
	CreatedAt   time.Time
}

func projectsHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := cfg.Sessions.UserID(r)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		ctx := withSessionUser(r.Context(), cfg, userID)

		switch r.Method {
		case http.MethodGet:
			renderProjects(w, ctx, cfg, userID, "", strings.TrimSpace(r.URL.Query().Get("type")))
		case http.MethodPost:
			handleProjectsPost(w, r.WithContext(ctx), cfg, userID)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func handleProjectsPost(w http.ResponseWriter, r *http.Request, cfg Config, userID string) {
	if err := r.ParseForm(); err != nil {
		renderProjects(w, r.Context(), cfg, userID, "bad form", "")
		return
	}
	switch r.FormValue("action") {
	case "create":
		req := verb.ProjectCreateRequest{
			Name:        r.FormValue("name"),
			Description: r.FormValue("description"),
			GitURL:      r.FormValue("git_url"),
		}
		body, _ := json.Marshal(req)
		if _, err := verb.Dispatch(r.Context(), "project_create", body); err != nil {
			arbor.WarnCtx(r.Context(), "projects: create", "user_id", userID, "err", err)
			renderProjects(w, r.Context(), cfg, userID, err.Error(), "")
			return
		}
		http.Redirect(w, r, "/projects", http.StatusSeeOther)
	default:
		renderProjects(w, r.Context(), cfg, userID, "unknown action", "")
	}
}

func renderProjects(w http.ResponseWriter, ctx context.Context, cfg Config, userID string, flashErr string, activeType string) {
	resp, err := verb.Dispatch(ctx, "project_list", nil)
	if err != nil {
		arbor.ErrorCtx(ctx, "projects: list", "user_id", userID, "err", err)
		http.Error(w, "could not list projects", http.StatusInternalServerError)
		return
	}
	var listResp verb.ProjectListResponse
	if err := json.Unmarshal(resp, &listResp); err != nil {
		arbor.ErrorCtx(ctx, "projects: decode list", "user_id", userID, "err", err)
		http.Error(w, "could not decode projects", http.StatusInternalServerError)
		return
	}
	// Build rows, applying the type filter, while collecting the full set of
	// types present (computed before filtering so clearing always restores).
	rows := make([]projectRow, 0, len(listResp.Projects))
	typeSet := map[string]bool{}
	for _, p := range listResp.Projects {
		if t := strings.TrimSpace(p.Type); t != "" {
			typeSet[t] = true
		}
		if activeType != "" && p.Type != activeType {
			continue
		}
		rows = append(rows, projectRow{
			ID:          p.ID,
			Name:        p.Name,
			Description: p.Description,
			Type:        p.Type,
			Phase:       p.Phase,
			GitURL:      p.GitURLCanonical,
			Status:      p.Status,
			CreatedAt:   p.CreatedAt,
		})
	}
	types := make([]projectTypeChip, 0, len(typeSet))
	for t := range typeSet {
		types = append(types, projectTypeChip{Type: t, Active: t == activeType})
	}
	sort.Slice(types, func(i, j int) bool { return types[i].Type < types[j].Type })

	var userEmail, userName, userAvatar string
	if cfg.Store != nil && cfg.Store.DB != nil {
		if u, err := cfg.Store.GetUserByID(ctx, userID); err == nil && u != nil {
			userEmail = u.Email
			userName = u.DisplayName
			userAvatar = avatarLetter(u.DisplayName, u.Email)
		}
	}

	data := projectsData{
		Title:       "projects · satellites",
		UserEmail:   userEmail,
		UserName:    userName,
		UserAvatar:  userAvatar,
		ActiveNav:   "projects",
		Projects:    rows,
		Types:       types,
		ActiveType:  activeType,
		FlashError:  flashErr,
		DevMode:     cfg.DevMode,
		FooterName:  footerName,
		FooterEmail: footerEmail,
		Version:     versionString(),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := projectsTmpl.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// withSessionUser stamps the session-resolved user onto ctx so verbs
// dispatched in-process (project_list / project_create) see the same
// auth.FromContext payload the /mcp middleware would provide.
func withSessionUser(ctx context.Context, cfg Config, userID string) context.Context {
	if cfg.Store == nil || cfg.Store.DB == nil {
		return ctx
	}
	u, err := cfg.Store.GetUserByID(ctx, userID)
	if err != nil || u == nil {
		return ctx
	}
	cfg.Store.TouchLastSeenAsync(u.ID) // portal access → last-seen (throttled)
	return auth.WithUser(ctx, u)
}
