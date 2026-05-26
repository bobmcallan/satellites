package server

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/verb"
)

var projectDetailTmpl = template.Must(
	template.New("project_detail.html").Funcs(template.FuncMap{
		"toggleSortURL": toggleSortURL,
		"toggleTagURL":  toggleTagURL,
		"clearTagURL":   clearTagURL,
		"formatTime":    formatRowTime,
	}).ParseFS(assets, "templates/project_detail.html"),
)

type projectDetailData struct {
	Title       string
	UserEmail   string
	Project     projectRow
	Stories     []storyRow
	ActiveTags  []string
	ActiveSort  string
	URLBase     string
	FlashError  string
	DevMode     bool
	FooterName  string
	FooterEmail string
	Version     string
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
	Summary            string
	SummaryUpdatedAt   *time.Time
	UpdatedAt          time.Time
	CreatedAt          time.Time
	Ledger             []ledgerRow
}

type ledgerRow struct {
	ID        string
	Kind      string
	Actor     string
	Body      string
	CreatedAt time.Time
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

		q := r.URL.Query()
		tags := q["tag"]
		stories, err := dispatchStoryList(ctx, projectID, tags)
		if err != nil {
			arbor.ErrorCtx(ctx, "project_detail: story_list", "id", projectID, "err", err)
			http.Error(w, "could not list stories", http.StatusInternalServerError)
			return
		}

		sortKey := q.Get("sort")
		applyStorySort(stories, sortKey)

		// Per-story ledger pre-fetch. Acceptable for the small story
		// counts a single project carries today; if a project ever
		// holds thousands of rows, swap to lazy expand-on-click.
		for i := range stories {
			entries, err := dispatchLedgerList(ctx, stories[i].ID, "")
			if err != nil {
				arbor.WarnCtx(ctx, "project_detail: ledger_list",
					"story_id", stories[i].ID, "err", err)
				continue
			}
			stories[i].Ledger = entries
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
			ActiveTags:  tags,
			ActiveSort:  sortKey,
			URLBase:     "/projects/" + projectID,
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

func dispatchStoryList(ctx context.Context, projectID string, tags []string) ([]storyRow, error) {
	// Post-unification (sty_0dd71f79) stories are documents-of-type-story.
	// document_list orders by created_at; the portal needs the legacy
	// story_list ordering (status → priority → recency), so dispatch
	// document_list with a custom limit and re-sort via the same store
	// method the verb registry uses internally. To keep the transport
	// boundary clean we just call document_list with type='story' and
	// project_id; ordering matches via documents indexes.
	body, _ := json.Marshal(verb.DocumentListRequest{
		Type:      "story",
		ProjectID: projectID,
		Tags:      tags,
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
			Tags: d.Tags, Summary: d.Summary, SummaryUpdatedAt: d.SummaryUpdatedAt,
			UpdatedAt: d.UpdatedAt, CreatedAt: d.CreatedAt,
		})
	}
	return out, nil
}

func dispatchLedgerList(ctx context.Context, storyID, kind string) ([]ledgerRow, error) {
	body, _ := json.Marshal(verb.LedgerListRequest{StoryID: storyID, Kind: kind})
	raw, err := verb.Dispatch(ctx, "ledger_list", body)
	if err != nil {
		return nil, err
	}
	var resp verb.LedgerListResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	out := make([]ledgerRow, 0, len(resp.Entries))
	for _, e := range resp.Entries {
		out = append(out, ledgerRow{
			ID: e.ID, Kind: e.Kind, Actor: e.Actor,
			Body: e.Body, CreatedAt: e.CreatedAt,
		})
	}
	return out, nil
}

// applyStorySort applies the user-selected primary sort on top of
// the canonical secondary order. Empty key keeps the default
// (status → priority → updated_at) the verb layer already returns.
func applyStorySort(rows []storyRow, key string) {
	switch key {
	case "id", "title", "status", "priority", "updated_at":
		// supported
	default:
		return
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		switch key {
		case "id":
			return a.ID < b.ID
		case "title":
			return strings.ToLower(a.Title) < strings.ToLower(b.Title)
		case "status":
			return statusRank(a.Status) < statusRank(b.Status)
		case "priority":
			return priorityRank(a.Priority) < priorityRank(b.Priority)
		case "updated_at":
			return a.UpdatedAt.After(b.UpdatedAt)
		}
		return false
	})
}

func statusRank(s string) int {
	switch s {
	case "in_progress":
		return 0
	case "ready":
		return 1
	case "review":
		return 2
	case "backlog":
		return 3
	case "done":
		return 4
	case "cancelled":
		return 5
	}
	return 6
}

func priorityRank(p string) int {
	switch p {
	case "critical":
		return 0
	case "high":
		return 1
	case "medium":
		return 2
	case "low":
		return 3
	}
	return 4
}

// toggleSortURL returns the query string for clicking the given
// sort header — re-clicking the active sort clears it (back to
// default). Tag filters are preserved.
func toggleSortURL(base string, activeSort, key string, tags []string) string {
	q := url.Values{}
	for _, t := range tags {
		q.Add("tag", t)
	}
	if activeSort != key {
		q.Set("sort", key)
	}
	if encoded := q.Encode(); encoded != "" {
		return base + "?" + encoded
	}
	return base
}

// toggleTagURL returns the query string for clicking a tag chip —
// re-clicking an active tag drops it from the filter.
func toggleTagURL(base, sortKey string, tags []string, clicked string) string {
	q := url.Values{}
	if sortKey != "" {
		q.Set("sort", sortKey)
	}
	present := false
	for _, t := range tags {
		if t == clicked {
			present = true
			continue
		}
		q.Add("tag", t)
	}
	if !present {
		q.Add("tag", clicked)
	}
	if encoded := q.Encode(); encoded != "" {
		return base + "?" + encoded
	}
	return base
}

// clearTagURL returns the URL with all tag filters dropped.
func clearTagURL(base, sortKey string) string {
	q := url.Values{}
	if sortKey != "" {
		q.Set("sort", sortKey)
	}
	if encoded := q.Encode(); encoded != "" {
		return base + "?" + encoded
	}
	return base
}

func formatRowTime(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04")
}
