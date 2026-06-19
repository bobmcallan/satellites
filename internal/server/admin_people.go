package server

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"

	"sort"
	"time"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/verb"
)

func sortWorkspaceRows(rows []peopleWorkspaceRow) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Name != rows[j].Name {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].ID < rows[j].ID
	})
}

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
	FlashLink   string

	WorkspaceID      string
	WorkspaceName    string
	IsWorkspaceAdmin bool
	IsGlobalAdmin    bool
	AllWorkspaces    []peopleWorkspaceRow
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

	// API-key management (sty_f972d934): the non-revoked keys scoped to the
	// workspace / selected project (admins only), plus the raw key shown ONCE
	// right after issue (IssuedKeyScope = "workspace" | "project").
	WorkspaceKeys  []verb.APIKeyRow
	ProjectKeys    []verb.APIKeyRow
	IssuedKey      string
	IssuedKeyScope string
}

type peopleMemberRow struct {
	UserID       string
	Email        string
	Name         string
	Role         string
	Source       string // "project" (explicit) | "workspace" (inherited admin)
	LastAccessed string // formatted last-seen, or "never"
}

type peopleProjectRow struct {
	ID   string
	Name string
}

type peopleWorkspaceRow struct {
	ID       string
	Name     string
	Selected bool
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
			renderAdminPeople(w, ctx, cfg, userID, r.URL.Query().Get("workspace_id"), r.URL.Query().Get("project_id"), "", "")
		case http.MethodPost:
			handleAdminPeoplePost(w, r.WithContext(ctx), cfg, userID)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func handleAdminPeoplePost(w http.ResponseWriter, r *http.Request, cfg Config, userID string) {
	if err := r.ParseForm(); err != nil {
		renderAdminPeople(w, r.Context(), cfg, userID, "", "", "bad form", "")
		return
	}
	ctx := r.Context()
	projectID := r.FormValue("project_id")
	wsID := r.FormValue("workspace_id")
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
			WorkspaceID: wsID, // land in the selected workspace, not the personal one
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
	case "pj_invite_link":
		// Generate a stored, redeemable link (no email) and surface it for
		// manual sending — render directly so the one-time link is shown.
		body, _ := json.Marshal(verb.InvitationCreateRequest{
			Scope: "project", ProjectID: projectID, Role: r.FormValue("role"),
		})
		raw, e := verb.Dispatch(ctx, "invitation_create", body)
		if e != nil {
			err = e
			break
		}
		var resp verb.InvitationCreateResponse
		_ = json.Unmarshal(raw, &resp)
		renderAdminPeople(w, ctx, cfg, userID, wsID, projectID, "", absoluteURL(r, resp.RedeemPath))
		return
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
	case "issue_ws_key", "issue_pj_key":
		// Mint a key scoped to the workspace or the project. apikey_create mints
		// the executor role only (sty_f972d934 keeps the safe default; elevating
		// is a server authz change deferred to its own story); the raw key is
		// shown ONCE — render directly so it is not lost to a redirect.
		req := verb.APIKeyCreateRequest{AgentName: r.FormValue("agent_name")}
		scope := "workspace"
		if action == "issue_ws_key" {
			req.WorkspaceID = wsID
		} else {
			req.ProjectID = projectID
			scope = "project"
		}
		raw, e := verb.Dispatch(ctx, "apikey_create", mustJSON(req))
		if e != nil {
			err = e
			break
		}
		var resp verb.APIKeyCreateResponse
		_ = json.Unmarshal(raw, &resp)
		renderAdminPeopleWithKey(w, ctx, cfg, userID, wsID, projectID, "", "", resp.APIKey, scope)
		return
	case "revoke_key":
		err = dispatch("apikey_revoke", verb.APIKeyRevokeRequest{KeyID: r.FormValue("key_id")})
	default:
		renderAdminPeople(w, ctx, cfg, userID, wsID, projectID, "unknown action", "")
		return
	}

	if err != nil {
		arbor.WarnCtx(ctx, "admin_people: action", "action", action, "user_id", userID, "err", err)
		renderAdminPeople(w, ctx, cfg, userID, wsID, projectID, err.Error(), "")
		return
	}
	q := url.Values{}
	if wsID != "" {
		q.Set("workspace_id", wsID)
	}
	if projectID != "" {
		q.Set("project_id", projectID)
	}
	dest := "/settings/people"
	if e := q.Encode(); e != "" {
		dest += "?" + e
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// listKeys returns the non-revoked API keys matching the filter (apikey_list is
// redacted — never the raw secret). A dispatch error yields an empty list; the
// page still renders (sty_f972d934).
func listKeys(ctx context.Context, req verb.APIKeyListRequest) []verb.APIKeyRow {
	raw, err := verb.Dispatch(ctx, "apikey_list", mustJSON(req))
	if err != nil {
		return nil
	}
	var resp verb.APIKeyListResponse
	if json.Unmarshal(raw, &resp) != nil {
		return nil
	}
	return resp.Keys
}

// absoluteURL builds a fully-qualified URL for path from the request, honouring
// a proxy's X-Forwarded-Proto. Used to render a copy-paste invite link.
func absoluteURL(r *http.Request, path string) string {
	scheme := "https"
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	} else if r.TLS == nil {
		scheme = "http"
	}
	return scheme + "://" + r.Host + path
}

func renderAdminPeople(w http.ResponseWriter, ctx context.Context, cfg Config, userID, requestedWS, projectID, flashErr, flashLink string) {
	renderAdminPeopleWithKey(w, ctx, cfg, userID, requestedWS, projectID, flashErr, flashLink, "", "")
}

// renderAdminPeopleWithKey is renderAdminPeople plus the one-time issued API key
// (sty_f972d934): the key secret is shown ONCE after issue and never re-fetched
// (apikey_list is redacted). issuedScope is "workspace" | "project" | "".
func renderAdminPeopleWithKey(w http.ResponseWriter, ctx context.Context, cfg Config, userID, requestedWS, projectID, flashErr, flashLink, issuedKey, issuedScope string) {
	data := adminPeopleData{
		Title:          "people · satellites",
		ActiveNav:      "people",
		DevMode:        cfg.DevMode,
		FooterName:     footerName,
		FooterEmail:    footerEmail,
		Version:        versionString(),
		FlashError:     flashErr,
		FlashLink:      flashLink,
		IssuedKey:      issuedKey,
		IssuedKeyScope: issuedScope,
		WorkspaceRoles: []string{"admin", "member"},
		ProjectRoles:   []string{"read", "write", "admin"},
	}
	if cfg.Store != nil && cfg.Store.DB != nil {
		if u, err := cfg.Store.GetUserByID(ctx, userID); err == nil && u != nil {
			data.UserEmail = u.Email
			data.UserName = u.DisplayName
			data.UserAvatar = avatarLetter(u.DisplayName, u.Email)
			data.IsGlobalAdmin = u.Role == auth.RoleAdmin
		}
	}

	// The workspaces the caller may operate (global admin → all; else their
	// own). Backs the selector (shown only to a global admin) and resolves the
	// selected workspace's name.
	nameMap := map[string]string{}
	if raw, err := verb.Dispatch(ctx, "workspace_list", nil); err == nil {
		var resp verb.WorkspaceListResponse
		if json.Unmarshal(raw, &resp) == nil {
			for _, ws := range resp.Workspaces {
				nameMap[ws.ID] = ws.Name
			}
		}
	}

	// Default to the viewer's personal workspace.
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

	// Honour ?workspace_id when the caller may see it: a global admin (any) or a
	// member of it (in nameMap). Admin powers there are still gated by
	// IsWorkspaceAdmin below.
	if requestedWS != "" && requestedWS != data.WorkspaceID && (data.IsGlobalAdmin || nameMap[requestedWS] != "") {
		data.WorkspaceID = requestedWS
		if n, ok := nameMap[requestedWS]; ok {
			data.WorkspaceName = n
		}
	}

	// Selector options (global admin only).
	if data.IsGlobalAdmin {
		for id, name := range nameMap {
			data.AllWorkspaces = append(data.AllWorkspaces, peopleWorkspaceRow{
				ID: id, Name: name, Selected: id == data.WorkspaceID,
			})
		}
		sortWorkspaceRows(data.AllWorkspaces)
	}

	// A global admin wields workspace-admin powers on any workspace, even
	// without a membership row there.
	data.IsWorkspaceAdmin = data.IsGlobalAdmin

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
		// Workspace-scoped API keys (admins only — issuing/revoking is an admin act).
		if data.IsWorkspaceAdmin {
			data.WorkspaceKeys = listKeys(ctx, verb.APIKeyListRequest{WorkspaceID: data.WorkspaceID})
		}
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
					row := peopleMemberRow{UserID: m.UserID, Role: m.Role, Source: m.Source}
					resolveUser(ctx, cfg, &row)
					data.ProjectMembers = append(data.ProjectMembers, row)
				}
			}
		}
		data.ProjectInvitations = listInvites(ctx, verb.InvitationListRequest{ProjectID: projectID, Status: "pending"})
		// Project-scoped API keys (admins only).
		if data.IsProjectAdmin {
			data.ProjectKeys = listKeys(ctx, verb.APIKeyListRequest{ProjectID: projectID})
		}
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
		row.LastAccessed = formatLastSeen(u.LastSeenAt)
	}
}

// formatLastSeen renders a last-seen timestamp for the member list: "never"
// when the user has not been seen, else a UTC minute-precision stamp.
func formatLastSeen(t *time.Time) string {
	if t == nil {
		return "never"
	}
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
