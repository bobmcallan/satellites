package server

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/verb"
)

var adminPeopleTmpl = template.Must(template.ParseFS(assets,
	"templates/admin_people.html", "templates/_user_menu.html"))

type adminPeopleData struct {
	Title       string
	UserEmail   string
	UserName    string
	UserAvatar  string
	ActiveNav   string
	DevMode     bool
	FooterName  string
	FooterEmail string
	Version     string
	FlashError  string

	WorkspaceID      string
	WorkspaceName    string
	IsWorkspaceAdmin bool
	Members          []peopleMemberRow
	Projects         []peopleProjectRow
	Invitations      []peopleInviteRow

	SelectedProjectID   string
	SelectedProjectName string
	IsProjectAdmin      bool
	ProjectMembers      []peopleMemberRow
	ProjectInvitations  []peopleInviteRow

	WorkspaceRoles []string
	ProjectRoles   []string
}

type peopleMemberRow struct {
	UserID string
	Email  string
	Name   string
	Role   string
}

type peopleProjectRow struct {
	ID   string
	Name string
}

type peopleInviteRow struct {
	ID    string
	Email string
	Role  string
}

func adminPeopleHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := cfg.Sessions.UserID(r)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		ctx := withSessionUser(r.Context(), cfg, userID)
		switch r.Method {
		case http.MethodGet:
			renderAdminPeople(w, ctx, cfg, userID, r.URL.Query().Get("project_id"), "")
		case http.MethodPost:
			handleAdminPeoplePost(w, r.WithContext(ctx), cfg, userID)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func handleAdminPeoplePost(w http.ResponseWriter, r *http.Request, cfg Config, userID string) {
	if err := r.ParseForm(); err != nil {
		renderAdminPeople(w, r.Context(), cfg, userID, "", "bad form")
		return
	}
	ctx := r.Context()
	projectID := r.FormValue("project_id")
	action := r.FormValue("action")

	dispatch := func(verbName string, req any) error {
		body, _ := json.Marshal(req)
		_, err := verb.Dispatch(ctx, verbName, body)
		return err
	}

	var err error
	switch action {
	case "create_project":
		err = dispatch("project_create", verb.ProjectCreateRequest{
			Name:        r.FormValue("name"),
			Description: r.FormValue("description"),
			GitURL:      r.FormValue("git_url"),
		})
		projectID = "" // stay on the workspace view
	case "ws_invite":
		err = dispatch("invitation_create", verb.InvitationCreateRequest{
			Email: r.FormValue("email"), Scope: "workspace",
			WorkspaceID: r.FormValue("workspace_id"), Role: r.FormValue("role"),
		})
	case "ws_member_role":
		err = dispatch("workspace_member_update_role", verb.WorkspaceMemberUpdateRequest{
			WorkspaceID: r.FormValue("workspace_id"), UserID: r.FormValue("user_id"), Role: r.FormValue("role"),
		})
	case "ws_member_remove":
		err = dispatch("workspace_member_remove", verb.WorkspaceMemberRemoveRequest{
			WorkspaceID: r.FormValue("workspace_id"), UserID: r.FormValue("user_id"),
		})
	case "pj_invite":
		err = dispatch("invitation_create", verb.InvitationCreateRequest{
			Email: r.FormValue("email"), Scope: "project",
			ProjectID: projectID, Role: r.FormValue("role"),
		})
	case "pj_member_role":
		err = dispatch("project_member_update_role", verb.ProjectMemberUpdateRequest{
			ProjectID: projectID, UserID: r.FormValue("user_id"), Role: r.FormValue("role"),
		})
	case "pj_member_remove":
		err = dispatch("project_member_remove", verb.ProjectMemberRemoveRequest{
			ProjectID: projectID, UserID: r.FormValue("user_id"),
		})
	case "invite_revoke":
		err = dispatch("invitation_revoke", verb.InvitationRevokeRequest{ID: r.FormValue("id")})
	default:
		renderAdminPeople(w, ctx, cfg, userID, projectID, "unknown action")
		return
	}

	if err != nil {
		arbor.WarnCtx(ctx, "admin_people: action", "action", action, "user_id", userID, "err", err)
		renderAdminPeople(w, ctx, cfg, userID, projectID, err.Error())
		return
	}
	dest := "/settings/people"
	if projectID != "" {
		dest += "?project_id=" + url.QueryEscape(projectID)
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

func renderAdminPeople(w http.ResponseWriter, ctx context.Context, cfg Config, userID, projectID, flashErr string) {
	data := adminPeopleData{
		Title:          "people · satellites",
		ActiveNav:      "people",
		DevMode:        cfg.DevMode,
		FooterName:     footerName,
		FooterEmail:    footerEmail,
		Version:        versionString(),
		FlashError:     flashErr,
		WorkspaceRoles: []string{"admin", "member"},
		ProjectRoles:   []string{"read", "write", "admin"},
	}
	if cfg.Store != nil && cfg.Store.DB != nil {
		if u, err := cfg.Store.GetUserByID(ctx, userID); err == nil && u != nil {
			data.UserEmail = u.Email
			data.UserName = u.DisplayName
			data.UserAvatar = avatarLetter(u.DisplayName, u.Email)
		}
	}

	// Resolve the viewer's personal workspace ("my workspace").
	if raw, err := verb.Dispatch(ctx, "workspace_personal", nil); err == nil {
		var ws struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &ws) == nil {
			data.WorkspaceID = ws.ID
			data.WorkspaceName = ws.Name
		}
	}

	if data.WorkspaceID != "" {
		// Workspace members — the viewer's role here decides IsWorkspaceAdmin.
		if raw, err := verb.Dispatch(ctx, "workspace_member_list",
			mustJSON(verb.WorkspaceMemberListRequest{WorkspaceID: data.WorkspaceID})); err == nil {
			var resp verb.WorkspaceMemberListResponse
			if json.Unmarshal(raw, &resp) == nil {
				for _, m := range resp.Members {
					row := peopleMemberRow{UserID: m.UserID, Role: m.Role}
					resolveUser(ctx, cfg, &row)
					data.Members = append(data.Members, row)
					if m.UserID == userID && (m.Role == "owner" || m.Role == "admin") {
						data.IsWorkspaceAdmin = true
					}
				}
			}
		}
		// Projects in the workspace.
		if raw, err := verb.Dispatch(ctx, "project_list",
			mustJSON(verb.ProjectListRequest{WorkspaceID: data.WorkspaceID})); err == nil {
			var resp verb.ProjectListResponse
			if json.Unmarshal(raw, &resp) == nil {
				for _, p := range resp.Projects {
					data.Projects = append(data.Projects, peopleProjectRow{ID: p.ID, Name: p.Name})
				}
			}
		}
		// Pending workspace invitations.
		data.Invitations = listInvites(ctx, verb.InvitationListRequest{WorkspaceID: data.WorkspaceID, Status: "pending"})
	}

	// Selected project pane.
	if projectID != "" {
		data.SelectedProjectID = projectID
		for _, p := range data.Projects {
			if p.ID == projectID {
				data.SelectedProjectName = p.Name
			}
		}
		// Effective access decides whether management controls render.
		if raw, err := verb.Dispatch(ctx, "project_access",
			mustJSON(verb.ProjectAccessRequest{ProjectID: projectID})); err == nil {
			var acc verb.ProjectAccessResponse
			if json.Unmarshal(raw, &acc) == nil {
				data.IsProjectAdmin = acc.Role == "admin"
			}
		}
		if raw, err := verb.Dispatch(ctx, "project_member_list",
			mustJSON(verb.ProjectMemberListRequest{ProjectID: projectID})); err == nil {
			var resp verb.ProjectMemberListResponse
			if json.Unmarshal(raw, &resp) == nil {
				for _, m := range resp.Members {
					row := peopleMemberRow{UserID: m.UserID, Role: m.Role}
					resolveUser(ctx, cfg, &row)
					data.ProjectMembers = append(data.ProjectMembers, row)
				}
			}
		}
		data.ProjectInvitations = listInvites(ctx, verb.InvitationListRequest{ProjectID: projectID, Status: "pending"})
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := adminPeopleTmpl.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func listInvites(ctx context.Context, req verb.InvitationListRequest) []peopleInviteRow {
	raw, err := verb.Dispatch(ctx, "invitation_list", mustJSON(req))
	if err != nil {
		return nil
	}
	var resp verb.InvitationListResponse
	if json.Unmarshal(raw, &resp) != nil {
		return nil
	}
	out := make([]peopleInviteRow, 0, len(resp.Invitations))
	for _, inv := range resp.Invitations {
		out = append(out, peopleInviteRow{ID: inv.ID, Email: inv.Email, Role: inv.Role})
	}
	return out
}

func resolveUser(ctx context.Context, cfg Config, row *peopleMemberRow) {
	if cfg.Store == nil || cfg.Store.DB == nil {
		return
	}
	if u, err := cfg.Store.GetUserByID(ctx, row.UserID); err == nil && u != nil {
		row.Email = u.Email
		row.Name = u.DisplayName
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
