package server

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"time"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/verb"
)

var workspacesTmpl = template.Must(template.ParseFS(assets, "templates/workspaces.html", "templates/_user_menu.html"))

type workspacesData struct {
	Title       string
	UserEmail   string
	UserName    string
	UserAvatar  string
	ActiveNav   string
	Workspaces  []workspaceRow
	DevMode     bool
	FooterName  string
	FooterEmail string
	Version     string
}

// workspaceRow is the list/detail card shape. The transport defines its own
// row (not the workspace domain struct) so this package imports no substrate
// domain package — the layering guard, same pattern as projectRow.
type workspaceRow struct {
	ID          string
	Name        string
	Description string
	Status      string
	IsDefault   bool
	CreatedAt   time.Time
}

// workspacesHandler renders the list of workspaces the caller can see
// (workspace_list is identity-filtered: a global admin sees all, others only
// their memberships). sty_67a66574.
func workspacesHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := cfg.Sessions.UserID(r)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		ctx := withSessionUser(r.Context(), cfg, userID)
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		renderWorkspaces(w, ctx, cfg, userID)
	}
}

func renderWorkspaces(w http.ResponseWriter, ctx context.Context, cfg Config, userID string) {
	resp, err := verb.Dispatch(ctx, "workspace_list", nil)
	if err != nil {
		arbor.ErrorCtx(ctx, "workspaces: list", "user_id", userID, "err", err)
		http.Error(w, "could not list workspaces", http.StatusInternalServerError)
		return
	}
	var listResp verb.WorkspaceListResponse
	if err := json.Unmarshal(resp, &listResp); err != nil {
		arbor.ErrorCtx(ctx, "workspaces: decode list", "user_id", userID, "err", err)
		http.Error(w, "could not decode workspaces", http.StatusInternalServerError)
		return
	}
	rows := make([]workspaceRow, 0, len(listResp.Workspaces))
	for _, ws := range listResp.Workspaces {
		rows = append(rows, workspaceRow{
			ID: ws.ID, Name: ws.Name, Status: ws.Status,
			IsDefault: ws.IsDefault, CreatedAt: ws.CreatedAt,
		})
	}

	var userEmail, userName, userAvatar string
	if cfg.Store != nil && cfg.Store.DB != nil {
		if u, err := cfg.Store.GetUserByID(ctx, userID); err == nil && u != nil {
			userEmail = u.Email
			userName = u.DisplayName
			userAvatar = avatarLetter(u.DisplayName, u.Email)
		}
	}

	data := workspacesData{
		Title:       "workspaces · satellites",
		UserEmail:   userEmail,
		UserName:    userName,
		UserAvatar:  userAvatar,
		ActiveNav:   "workspaces",
		Workspaces:  rows,
		DevMode:     cfg.DevMode,
		FooterName:  footerName,
		FooterEmail: footerEmail,
		Version:     versionString(),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := workspacesTmpl.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
