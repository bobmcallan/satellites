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

// libraryHandler renders /library — the global skill-library browse page
// (epic:workspace-agents, sty_b2f77307). It lists library-scope skills with a
// server-side kind filter (?kind=<k>) mirroring the stories panel's filter.
func libraryHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := cfg.Sessions.UserID(r)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		ctx := withSessionUser(r.Context(), cfg, userID)

		activeKind := strings.TrimSpace(r.URL.Query().Get("kind"))
		skills, kinds, err := loadLibrarySkills(ctx, activeKind)
		if err != nil {
			arbor.ErrorCtx(ctx, "library: load skills", "user_id", userID, "err", err)
			http.Error(w, "could not list library skills", http.StatusInternalServerError)
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

// loadLibrarySkills lists library-scope skills via document_list, enriches each
// with kind + description from its frontmatter (document_get), applies the kind
// filter, and returns the filtered rows plus the full set of kinds present (for
// the filter chips, computed before filtering so clearing always restores).
func loadLibrarySkills(ctx context.Context, activeKind string) ([]librarySkillRow, []libraryKindChip, error) {
	listBody, _ := json.Marshal(map[string]string{"type": "skill", "scope": "library"})
	raw, err := verb.Dispatch(ctx, "document_list", listBody)
	if err != nil {
		return nil, nil, err
	}
	var listResp struct {
		Items []struct {
			Name      string   `json:"name"`
			ProjectID string   `json:"project_id"`
			Tags      []string `json:"tags"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &listResp); err != nil {
		return nil, nil, err
	}

	rows := make([]librarySkillRow, 0, len(listResp.Items))
	kindSet := map[string]bool{}
	for _, it := range listResp.Items {
		kind := kindFromTags(it.Tags)
		desc := ""
		// Enrich from the skill's frontmatter (library is public-read). A
		// failed read degrades to an empty description, never a page error.
		getBody, _ := json.Marshal(map[string]string{"scope": "library", "name": it.Name, "project_id": it.ProjectID})
		if graw, gerr := verb.Dispatch(ctx, "document_get", getBody); gerr == nil {
			var gr struct {
				RawBody string `json:"raw_body"`
			}
			if json.Unmarshal(graw, &gr) == nil {
				if fm, _, ferr := frontmatter.Parse([]byte(gr.RawBody)); ferr == nil {
					desc = fm.Description
					if strings.TrimSpace(fm.Kind) != "" {
						kind = fm.Kind
					}
				}
			}
		}
		if kind != "" {
			kindSet[kind] = true
		}
		if activeKind != "" && kind != activeKind {
			continue
		}
		rows = append(rows, librarySkillRow{Name: it.Name, Kind: kind, Description: desc, Publisher: it.ProjectID})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })

	kinds := make([]libraryKindChip, 0, len(kindSet))
	for k := range kindSet {
		kinds = append(kinds, libraryKindChip{Kind: k, Active: k == activeKind})
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i].Kind < kinds[j].Kind })
	return rows, kinds, nil
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
