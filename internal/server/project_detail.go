package server

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/verb"
)

var projectDetailTmpl = template.Must(
	template.New("project_detail.html").Funcs(template.FuncMap{
		"formatTime": formatRowTime,
		"list":       templateList,
	}).ParseFS(assets, "templates/project_detail.html"),
)

// templateList is a html/template helper that builds a []string for
// range — used to enumerate the status enum inline in the template
// instead of redeclaring it in Go and threading it through.
func templateList(items ...string) []string { return items }

type projectDetailData struct {
	Title       string
	UserEmail   string
	Project     projectRow
	Stories     []storyRow
	Paginator   paginatorData
	DevMode     bool
	FooterName  string
	FooterEmail string
	Version     string
}

// paginatorData carries the page-based paginator state the template
// needs to render the prev/next strip + "page N of M" indicator. Total
// is over the whole pre-filtered story set; the panel's client-side
// chip filtering operates inside the current server page.
type paginatorData struct {
	Total     int
	Page      int
	PageSize  int
	PageCount int
	HasPrev   bool
	HasNext   bool
	PrevPage  int
	NextPage  int
	PageQuery string // pre-encoded "stories_page_size=N" suffix
}

type storyRow struct {
	ID                 string
	ParentID           string
	Title              string
	Body               string
	AcceptanceCriteria string
	Status             string
	Priority           string
	Category           string
	Tags               []string
	UpdatedAt          time.Time
	CreatedAt          time.Time
}

func projectDetailHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := cfg.Sessions.UserID(r)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		ctx := withSessionUser(r.Context(), cfg, userID)

		projectID := strings.TrimSpace(path.Base(r.URL.Path))
		if projectID == "" || projectID == "projects" {
			http.NotFound(w, r)
			return
		}

		pj, err := dispatchProjectGet(ctx, projectID)
		if err != nil {
			arbor.WarnCtx(ctx, "project_detail: project_get", "id", projectID, "err", err)
			http.NotFound(w, r)
			return
		}

		all, err := dispatchStoryList(ctx, projectID)
		if err != nil {
			arbor.ErrorCtx(ctx, "project_detail: story_list", "id", projectID, "err", err)
			http.Error(w, "could not list stories", http.StatusInternalServerError)
			return
		}

		// Slice the result set into the requested page before fetching
		// bodies — saves round-trips on large projects.
		page, pageSize := parseStoryPagination(r.URL.Query())
		stories, paginator := paginate(all, page, pageSize)

		// Per-story body fetch so the expanded row can render the
		// description section. Loop only over the visible slice.
		for i := range stories {
			body, err := dispatchStoryBody(ctx, stories[i].ID)
			if err != nil {
				arbor.WarnCtx(ctx, "project_detail: story_body",
					"story_id", stories[i].ID, "err", err)
				continue
			}
			stories[i].Body = body
		}

		var userEmail string
		if cfg.Store != nil && cfg.Store.DB != nil {
			if u, err := cfg.Store.GetUserByID(ctx, userID); err == nil && u != nil {
				userEmail = u.Email
			}
		}

		data := projectDetailData{
			Title: pj.Name + " · projects · satellites",
			Project: projectRow{
				ID: pj.ID, Name: pj.Name, Description: pj.Description,
				GitURL: pj.GitURLCanonical, Status: pj.Status, CreatedAt: pj.CreatedAt,
			},
			UserEmail:   userEmail,
			Stories:     stories,
			Paginator:   paginator,
			DevMode:     cfg.DevMode,
			FooterName:  footerName,
			FooterEmail: footerEmail,
			Version:     versionString(),
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := projectDetailTmpl.Execute(w, data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// projectDetailRow is the local shape this handler needs from
// project_get. Defined here (not imported from internal/project) so
// the transport stays decoupled from the domain package — the
// layering guard enforces that.
type projectDetailRow struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	GitURLCanonical string    `json:"git_url_canonical"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
}

func dispatchProjectGet(ctx context.Context, id string) (projectDetailRow, error) {
	body, _ := json.Marshal(verb.ProjectGetRequest{ID: id})
	raw, err := verb.Dispatch(ctx, "project_get", body)
	if err != nil {
		return projectDetailRow{}, err
	}
	var p projectDetailRow
	if err := json.Unmarshal(raw, &p); err != nil {
		return projectDetailRow{}, err
	}
	return p, nil
}

func dispatchStoryList(ctx context.Context, projectID string) ([]storyRow, error) {
	// Post-unification (sty_0dd71f79) stories are documents-of-type-story.
	// The portal pulls every story for the project; the V4-style panel
	// filters client-side, so we no longer thread tag filters through
	// the URL.
	body, _ := json.Marshal(verb.DocumentListRequest{
		Type:      "story",
		ProjectID: projectID,
		Limit:     200,
	})
	raw, err := verb.Dispatch(ctx, "document_list", body)
	if err != nil {
		return nil, err
	}
	var resp verb.DocumentListResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	out := make([]storyRow, 0, len(resp.Items))
	for _, d := range resp.Items {
		out = append(out, storyRow{
			ID: d.ID, ParentID: d.ParentID, Title: d.Name,
			AcceptanceCriteria: d.AcceptanceCriteria,
			Status:             d.Status, Priority: d.Priority, Category: d.Category,
			Tags: d.Tags, UpdatedAt: d.UpdatedAt, CreatedAt: d.CreatedAt,
		})
	}
	return out, nil
}

// dispatchStoryBody fetches the latest body for a story via
// document_get. Errors are returned to the caller; the handler logs +
// continues so one missing body never breaks the panel.
func dispatchStoryBody(ctx context.Context, id string) (string, error) {
	body, _ := json.Marshal(verb.DocumentGetRequest{ID: id})
	raw, err := verb.Dispatch(ctx, "document_get", body)
	if err != nil {
		return "", err
	}
	var resp verb.DocumentGetResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", err
	}
	return resp.RawBody, nil
}

func formatRowTime(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04")
}

// storyPageSizeDefault is the default page size when ?stories_page_size
// is absent. storyPageSizeMax is the upper clamp — aligned with
// document_list's own Limit ceiling so we never exceed what the verb
// will return in a single fetch.
const (
	storyPageSizeDefault = 50
	storyPageSizeMax     = 200
)

// parseStoryPagination reads stories_page + stories_page_size off the
// request URL with the documented defaults + clamps. Invalid values
// fall back to defaults rather than failing the request — the
// paginator is a navigation aid, not the source of truth.
func parseStoryPagination(q url.Values) (page, pageSize int) {
	pageSize = storyPageSizeDefault
	if raw := strings.TrimSpace(q.Get("stories_page_size")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			pageSize = n
		}
	}
	if pageSize < 1 {
		pageSize = 1
	}
	if pageSize > storyPageSizeMax {
		pageSize = storyPageSizeMax
	}
	page = 1
	if raw := strings.TrimSpace(q.Get("stories_page")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			page = n
		}
	}
	return page, pageSize
}

// paginate slices `all` into the requested page and returns the slice
// alongside the paginator state the template renders. Out-of-range
// pages clamp to the last page so a stale URL still renders something
// sensible.
func paginate(all []storyRow, page, pageSize int) ([]storyRow, paginatorData) {
	total := len(all)
	pageCount := (total + pageSize - 1) / pageSize
	if pageCount < 1 {
		pageCount = 1
	}
	if page > pageCount {
		page = pageCount
	}
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	slice := all[start:end]
	p := paginatorData{
		Total:     total,
		Page:      page,
		PageSize:  pageSize,
		PageCount: pageCount,
		HasPrev:   page > 1,
		HasNext:   page < pageCount,
		PrevPage:  page - 1,
		NextPage:  page + 1,
		PageQuery: "stories_page_size=" + strconv.Itoa(pageSize),
	}
	return slice, p
}
