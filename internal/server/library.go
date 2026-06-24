package server

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"sort"
	"strings"

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
	Skills      []librarySkillRow
	Kinds       []libraryKindChip
	ActiveKind  string
	DevMode     bool
	FooterName  string
	FooterEmail string
	Version     string
}

type librarySkillRow struct {
	Name        string
	Kind        string
	Description string
	Publisher   string
}

type libraryKindChip struct {
	Kind   string
	Active bool
}

// libraryHandler renders /library — the global library browse page
// (epic:workspace-agents, sty_b2f77307; tasks added sty_35d8e166). It lists
// library-scope skills AND tasks in one table with a server-side kind filter
// (?kind=<k>) mirroring the stories panel's filter — skills carry their kind
// (default "skill"), tasks carry "task", so the chips give a task-vs-skill split.
func libraryHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := cfg.Sessions.UserID(r)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		ctx := withSessionUser(r.Context(), cfg, userID)

		activeKind := strings.TrimSpace(r.URL.Query().Get("kind"))
		skills, kinds, err := loadLibraryRows(ctx, activeKind)
		if err != nil {
			arbor.ErrorCtx(ctx, "library: load rows", "user_id", userID, "err", err)
			http.Error(w, "could not list library artifacts", http.StatusInternalServerError)
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
			Title:       "skill library · satellites",
			UserEmail:   userEmail,
			UserName:    userName,
			UserAvatar:  userAvatar,
			ActiveNav:   "library",
			Skills:      skills,
			Kinds:       kinds,
			ActiveKind:  activeKind,
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

// loadLibraryRows lists library-scope skills AND tasks, enriches each with kind +
// description, applies the kind filter, and returns the filtered rows plus the
// full set of kinds present (chips, computed before filtering so clearing always
// restores). A skill's kind is its `kind:` tag / frontmatter kind, defaulting to
// "skill"; a task's kind is always "task" — so the chips give a task-vs-skill
// split (sty_35d8e166). A failed per-item read degrades to an empty description,
// never a page error.
func loadLibraryRows(ctx context.Context, activeKind string) ([]librarySkillRow, []libraryKindChip, error) {
	skills, err := listLibraryDocs(ctx, "skill")
	if err != nil {
		return nil, nil, err
	}
	tasks, err := listLibraryDocs(ctx, "task")
	if err != nil {
		return nil, nil, err
	}

	rows := make([]librarySkillRow, 0, len(skills)+len(tasks))
	kindSet := map[string]bool{}
	add := func(items []libraryDocItem, forceKind, defaultKind string) {
		for _, it := range items {
			desc, fmKind := "", ""
			getBody, _ := json.Marshal(map[string]string{"scope": "library", "name": it.Name, "project_id": it.ProjectID})
			if graw, gerr := verb.Dispatch(ctx, "document_get", getBody); gerr == nil {
				var gr struct {
					RawBody string `json:"raw_body"`
				}
				if json.Unmarshal(graw, &gr) == nil {
					if fm, _, ferr := frontmatter.Parse([]byte(gr.RawBody)); ferr == nil {
						desc, fmKind = fm.Description, fm.Kind
					}
				}
			}
			kind := libraryRowKind(forceKind, it.Tags, fmKind, defaultKind)
			if kind != "" {
				kindSet[kind] = true
			}
			if activeKind != "" && kind != activeKind {
				continue
			}
			rows = append(rows, librarySkillRow{Name: it.Name, Kind: kind, Description: desc, Publisher: it.ProjectID})
		}
	}
	add(skills, "", "skill") // skills keep their own kind, defaulting to "skill"
	add(tasks, "task", "task")
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Kind != rows[j].Kind {
			return rows[i].Kind < rows[j].Kind
		}
		return rows[i].Name < rows[j].Name
	})

	kinds := make([]libraryKindChip, 0, len(kindSet))
	for k := range kindSet {
		kinds = append(kinds, libraryKindChip{Kind: k, Active: k == activeKind})
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i].Kind < kinds[j].Kind })
	return rows, kinds, nil
}

// libraryRowKind resolves the displayed/filterable kind of a library row. A
// non-empty forceKind wins outright (a task is always "task"). Otherwise the
// `kind:` tag, then a frontmatter kind override, then defaultKind when both are
// empty — so a skill with no declared kind still falls into the "skill" chip,
// giving a clean task-vs-skill split (sty_35d8e166).
func libraryRowKind(forceKind string, tags []string, fmKind, defaultKind string) string {
	if strings.TrimSpace(forceKind) != "" {
		return forceKind
	}
	kind := kindFromTags(tags)
	if strings.TrimSpace(fmKind) != "" {
		kind = fmKind
	}
	if strings.TrimSpace(kind) == "" {
		kind = defaultKind
	}
	return kind
}

// kindFromTags extracts a skill's kind from its `kind:<k>` tag, "" if absent.
func kindFromTags(tags []string) string {
	for _, t := range tags {
		if strings.HasPrefix(t, "kind:") {
			return strings.TrimPrefix(t, "kind:")
		}
	}
	return ""
}
