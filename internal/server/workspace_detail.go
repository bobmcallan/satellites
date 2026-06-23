package server

import (
	"encoding/json"
	"html/template"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/verb"
)

var workspaceDetailTmpl = template.Must(template.ParseFS(assets, "templates/workspace_detail.html", "templates/_user_menu.html"))

type workspaceDetailData struct {
	Title         string
	UserEmail     string
	UserName      string
	UserAvatar    string
	ActiveNav     string
	Workspace     workspaceRow
	SeedMD        string
	ObjectiveBody string
	Projects      []workspaceProjectRow
	Mounts        []workspaceMountRow
	Documents     []workspaceDocRow
	Members       []workspaceMemberRow
	DevMode       bool
	FooterName    string
	FooterEmail   string
	Version       string
}

// workspaceProjectRow is a home repo of the workspace, annotated with the
// caller's effective access role (from project_list's Roles map).
type workspaceProjectRow struct {
	ID          string
	Name        string
	Description string
	Type        string
	Phase       string
	Status      string
	Role        string
}

// workspaceDocRow is one workspace-scoped document (the engagement corpus)
// rendered in the page's documents section.
type workspaceDocRow struct {
	ID        string
	Name      string
	UpdatedAt time.Time
}

// workspaceMountRow is one readonly repo mounted into the workspace (home
// elsewhere), rendered in the page's mounted-repos section (sty_1ede3853).
type workspaceMountRow struct {
	ProjectID string
	Name      string
	Status    string
}

// workspaceMemberRow is one member of the workspace (user + role), rendered in
// the page's members section so the page reflects allocation (sty_20687710).
type workspaceMemberRow struct {
	UserID string
	Name   string
	Role   string
}

// workspaceDetailRow is the local shape this handler needs from workspace_get.
// Defined here (not imported from internal/workspace) so the transport stays
// decoupled from the domain package — the layering guard, same pattern as
// projectDetailRow.
type workspaceDetailRow struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Status      string    `json:"status"`
	IsDefault   bool      `json:"is_default"`
	CreatedAt   time.Time `json:"created_at"`
	SeedMD      string    `json:"seed_md"`
}

// workspaceDetailHandler renders one workspace: its name + meta, the objective
// placeholder (filled by epic-order:6), and its home repos with the caller's
// access role. project_list is authz-gated, so a caller who cannot list the
// workspace's projects falls through to 404 (no leak). sty_67a66574.
func workspaceDetailHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := cfg.Sessions.UserID(r)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		ctx := withSessionUser(r.Context(), cfg, userID)

		wsID := strings.TrimSpace(path.Base(r.URL.Path))
		if wsID == "" || wsID == "workspaces" {
			http.NotFound(w, r)
			return
		}

		wsReq, _ := json.Marshal(verb.WorkspaceGetRequest{ID: wsID})
		wsResp, err := verb.Dispatch(ctx, "workspace_get", wsReq)
		if err != nil {
			arbor.WarnCtx(ctx, "workspace_detail: workspace_get", "id", wsID, "err", err)
			http.NotFound(w, r)
			return
		}
		var ws workspaceDetailRow
		if err := json.Unmarshal(wsResp, &ws); err != nil {
			arbor.ErrorCtx(ctx, "workspace_detail: decode workspace", "id", wsID, "err", err)
			http.Error(w, "could not decode workspace", http.StatusInternalServerError)
			return
		}

		plReq, _ := json.Marshal(verb.ProjectListRequest{WorkspaceID: wsID})
		plResp, err := verb.Dispatch(ctx, "project_list", plReq)
		if err != nil {
			// Forbidden (caller not a member) or any list error → 404, so a
			// non-member never sees the workspace's repos.
			arbor.WarnCtx(ctx, "workspace_detail: project_list", "id", wsID, "err", err)
			http.NotFound(w, r)
			return
		}
		var pl verb.ProjectListResponse
		if err := json.Unmarshal(plResp, &pl); err != nil {
			arbor.ErrorCtx(ctx, "workspace_detail: decode projects", "id", wsID, "err", err)
			http.Error(w, "could not decode projects", http.StatusInternalServerError)
			return
		}
		projects := make([]workspaceProjectRow, 0, len(pl.Projects))
		for _, p := range pl.Projects {
			projects = append(projects, workspaceProjectRow{
				ID: p.ID, Name: p.Name, Description: p.Description, Type: p.Type,
				Phase: p.Phase, Status: p.Status, Role: pl.Roles[p.ID],
			})
		}

		// Synthesized objective (sty_a0099c04): the workspace document named
		// "objective" if present. Best-effort — absence leaves the placeholder.
		var objectiveBody string
		objReq, _ := json.Marshal(verb.DocumentGetRequest{Scope: "workspace", WorkspaceID: wsID, Name: "objective"})
		if objResp, err := verb.Dispatch(ctx, "document_get", objReq); err == nil {
			var obj verb.DocumentGetResponse
			if err := json.Unmarshal(objResp, &obj); err == nil {
				objectiveBody = strings.TrimSpace(obj.RawBody)
			}
		}

		// Readonly mounts: repos whose home is elsewhere, referenced here
		// read-only (sty_1ede3853). Best-effort, like the doc list below.
		var mounts []workspaceMountRow
		mtReq, _ := json.Marshal(verb.WorkspaceMountListRequest{WorkspaceID: wsID})
		if mtResp, err := verb.Dispatch(ctx, "workspace_mount_list", mtReq); err != nil {
			arbor.WarnCtx(ctx, "workspace_detail: workspace_mount_list", "id", wsID, "err", err)
		} else {
			var ml verb.WorkspaceMountListResponse
			if err := json.Unmarshal(mtResp, &ml); err == nil {
				for _, m := range ml.Mounts {
					mounts = append(mounts, workspaceMountRow{ProjectID: m.ProjectID, Name: m.Name, Status: m.Status})
				}
			}
		}

		// Workspace corpus: the workspace-scoped documents (sty_3c2f02bf).
		// Best-effort — a list error leaves the section empty rather than
		// failing the page.
		var docs []workspaceDocRow
		dlReq, _ := json.Marshal(verb.DocumentListRequest{
			Scope: "workspace", WorkspaceID: wsID, Type: "document", Limit: 200,
		})
		if dlResp, err := verb.Dispatch(ctx, "document_list", dlReq); err != nil {
			arbor.WarnCtx(ctx, "workspace_detail: document_list", "id", wsID, "err", err)
		} else {
			var dl verb.DocumentListResponse
			if err := json.Unmarshal(dlResp, &dl); err == nil {
				for _, d := range dl.Items {
					docs = append(docs, workspaceDocRow{ID: d.ID, Name: d.Name, UpdatedAt: d.UpdatedAt})
				}
			}
		}

		// Workspace members (sty_20687710): the roster the page reflects after an
		// allocation. Best-effort — a non-member caller (forbidden) leaves it empty.
		var members []workspaceMemberRow
		mlReq, _ := json.Marshal(verb.WorkspaceMemberListRequest{WorkspaceID: wsID})
		if mlResp, err := verb.Dispatch(ctx, "workspace_member_list", mlReq); err != nil {
			arbor.WarnCtx(ctx, "workspace_detail: workspace_member_list", "id", wsID, "err", err)
		} else {
			var ml verb.WorkspaceMemberListResponse
			if err := json.Unmarshal(mlResp, &ml); err == nil {
				for _, m := range ml.Members {
					name := m.Name
					if name == "" {
						name = m.UserID
					}
					members = append(members, workspaceMemberRow{UserID: m.UserID, Name: name, Role: m.Role})
				}
			}
		}

		var userEmail, userName, userAvatar string
		if cfg.Store != nil && cfg.Store.DB != nil {
			if u, err := cfg.Store.GetUserByID(ctx, userID); err == nil && u != nil {
				userEmail = u.Email
				userName = u.DisplayName
				userAvatar = avatarLetter(u.DisplayName, u.Email)
			}
		}

		data := workspaceDetailData{
			Title: ws.Name + " · workspaces · satellites",
			Workspace: workspaceRow{
				ID: ws.ID, Name: ws.Name, Description: ws.Description, Status: ws.Status,
				IsDefault: ws.IsDefault, CreatedAt: ws.CreatedAt,
			},
			SeedMD:        ws.SeedMD,
			ObjectiveBody: objectiveBody,
			Projects:      projects,
			Mounts:        mounts,
			Documents:     docs,
			Members:       members,
			UserEmail:     userEmail,
			UserName:      userName,
			UserAvatar:    userAvatar,
			ActiveNav:     "workspaces",
			DevMode:       cfg.DevMode,
			FooterName:    footerName,
			FooterEmail:   footerEmail,
			Version:       versionString(),
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := workspaceDetailTmpl.Execute(w, data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}
