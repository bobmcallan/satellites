package server

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"sort"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/frontmatter"
	"github.com/bobmcallan/satellites/internal/verb"
)

var libraryTmpl = template.Must(
	template.New("library.html").ParseFS(assets, "templates/library.html", "templates/_user_menu.html"),
)

type libraryData struct {
	Title       string
	UserEmail   string
	UserName    string
	UserAvatar  string
	ActiveNav   string
	Tasks       []libraryTaskRow
	DevMode     bool
	FooterName  string
	FooterEmail string
	Version     string
}

type libraryTaskRow struct {
	Name        string
	Description string
	Publisher   string
}

// libraryHandler renders /library — the global library browse page
// (epic:workspace-agents, sty_b2f77307). The library surface is published TASKS
// ONLY (sty_98956dbb): a library-scope task is the published COPY of a project
// task, adoptable across workspaces. Skills, gates/reviewers, and workflows stay
// project/user-local and never list here.
func libraryHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := cfg.Sessions.UserID(r)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		ctx := withSessionUser(r.Context(), cfg, userID)

		tasks, err := loadLibraryTasks(ctx)
		if err != nil {
			arbor.ErrorCtx(ctx, "library: load tasks", "user_id", userID, "err", err)
			http.Error(w, "could not list library tasks", http.StatusInternalServerError)
			return
		}

		var userEmail, userName, userAvatar string
		if cfg.Store != nil && cfg.Store.DB != nil {
			if u, err := cfg.Store.GetUserByID(ctx, userID); err == nil && u != nil {
				userEmail = u.Email
				userName = u.DisplayName
				userAvatar = avatarLetter(u.DisplayName, u.Email)
			}
		}

		data := libraryData{
			Title:       "task library · satellites",
			UserEmail:   userEmail,
			UserName:    userName,
			UserAvatar:  userAvatar,
			ActiveNav:   "library",
			Tasks:       tasks,
			DevMode:     cfg.DevMode,
			FooterName:  footerName,
			FooterEmail: footerEmail,
			Version:     versionString(),
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := libraryTmpl.Execute(w, data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// libraryDocItem is one library-scope artifact as document_list returns it.
type libraryDocItem struct {
	Name      string   `json:"name"`
	ProjectID string   `json:"project_id"`
	Tags      []string `json:"tags"`
}

// listLibraryDocs lists library-scope documents of one type (skill | task). A
// type:task at scope:library is the published distribution artifact, and
// document_list keeps library rows when the request itself is scope:library
// (dropForeignLibraryTasks only strips them from project-scope listings).
func listLibraryDocs(ctx context.Context, docType string) ([]libraryDocItem, error) {
	listBody, _ := json.Marshal(map[string]string{"type": docType, "scope": "library"})
	raw, err := verb.Dispatch(ctx, "document_list", listBody)
	if err != nil {
		return nil, err
	}
	var listResp struct {
		Items []libraryDocItem `json:"items"`
	}
	if err := json.Unmarshal(raw, &listResp); err != nil {
		return nil, err
	}
	return listResp.Items, nil
}

// loadLibraryTasks lists library-scope TASKS only and enriches each with its
// frontmatter description (sty_98956dbb). A library-scope task is the published
// distribution copy of a project task; skills, gates/reviewers, and workflows
// are never listed — they stay project/user-local. A failed per-item read
// degrades to an empty description, never a page error.
func loadLibraryTasks(ctx context.Context) ([]libraryTaskRow, error) {
	tasks, err := listLibraryDocs(ctx, "task")
	if err != nil {
		return nil, err
	}
	rows := make([]libraryTaskRow, 0, len(tasks))
	for _, it := range tasks {
		desc := ""
		getBody, _ := json.Marshal(map[string]string{"scope": "library", "name": it.Name, "project_id": it.ProjectID})
		if graw, gerr := verb.Dispatch(ctx, "document_get", getBody); gerr == nil {
			var gr struct {
				RawBody string `json:"raw_body"`
			}
			if json.Unmarshal(graw, &gr) == nil {
				if fm, _, ferr := frontmatter.Parse([]byte(gr.RawBody)); ferr == nil {
					desc = fm.Description
				}
			}
		}
		rows = append(rows, libraryTaskRow{Name: it.Name, Description: desc, Publisher: it.ProjectID})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows, nil
}
