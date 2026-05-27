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
	}).ParseFS(assets, "templates/project_detail.html", "templates/_user_menu.html"),
)

// templateList is a html/template helper that builds a []string for
// range — used to enumerate the status enum inline in the template
// instead of redeclaring it in Go and threading it through.
func templateList(items ...string) []string { return items }

type projectDetailData struct {
	Title       string
	UserEmail   string
	UserName    string
	UserAvatar  string
	ActiveNav   string
	Project     projectRow
	Stories     []storyRow
	StoryShown  int // initial visible count = len(Stories); Alpine x-text overwrites on filter change
	StoryTotal  int // project-wide total from document_count; 0 = unavailable
	Paginator   paginatorData
	DevMode     bool
	FooterName  string
	FooterEmail string
	Version     string
}

// paginatorData carries the cursor-paginator state the template
// renders. Total + PageCount come from document_count (sty_cc20c0f5)
// alongside dispatchStoryList; if that call fails the template falls
// back to a "page N" rendering with no total.
type paginatorData struct {
	Page      int
	PageCount int // 0 = unknown; renders as "page N" instead of "page N of M"
	Total     int // unfiltered project-wide story count
	HasPrev   bool
	HasNext   bool
	PrevURL   string
	NextURL   string
	PageSize  int // surfaced for diagnostics + debug overlay
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

		q := r.URL.Query()
		pageSize := readStoryPageSize(ctx)
		cursor := q.Get("stories_cursor")
		page := 1
		if p := strings.TrimSpace(q.Get("stories_page")); p != "" {
			if n, err := strconv.Atoi(p); err == nil && n > 0 {
				page = n
			}
		}
		backStack := readBackStack(q)

		stories, nextCursor, err := dispatchStoryList(ctx, projectID, cursor, pageSize)
		if err != nil {
			arbor.ErrorCtx(ctx, "project_detail: story_list", "id", projectID, "err", err)
			http.Error(w, "could not list stories", http.StatusInternalServerError)
			return
		}

		// document_count for the total + PageCount. Failure here
		// degrades the indicator to "page N" without a total, but the
		// page still renders. Path B from sty_975afe24's body: the
		// count is project-wide and does NOT reflect the panel's
		// client-side chip filter — the indicator label spells this
		// out so operators aren't misled.
		total := dispatchStoryCount(ctx, projectID)

		paginator := paginatorData{
			Page:     page,
			Total:    total,
			HasPrev:  len(backStack) > 0,
			HasNext:  nextCursor != "",
			PageSize: pageSize,
		}
		if total > 0 && pageSize > 0 {
			paginator.PageCount = (total + pageSize - 1) / pageSize
			if paginator.PageCount < 1 {
				paginator.PageCount = 1
			}
		}
		base := "/projects/" + projectID
		if paginator.HasPrev {
			paginator.PrevURL = prevPageURL(base, page, backStack)
		}
		if paginator.HasNext {
			paginator.NextURL = nextPageURL(base, page, cursor, nextCursor, backStack)
		}

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

		var userEmail, userName, userAvatar string
		if cfg.Store != nil && cfg.Store.DB != nil {
			if u, err := cfg.Store.GetUserByID(ctx, userID); err == nil && u != nil {
				userEmail = u.Email
				userName = u.DisplayName
				userAvatar = avatarLetter(u.DisplayName, u.Email)
			}
		}

		data := projectDetailData{
			Title: pj.Name + " · projects · satellites",
			Project: projectRow{
				ID: pj.ID, Name: pj.Name, Description: pj.Description,
				GitURL: pj.GitURLCanonical, Status: pj.Status, CreatedAt: pj.CreatedAt,
			},
			UserEmail:   userEmail,
			UserName:    userName,
			UserAvatar:  userAvatar,
			ActiveNav:   "projects",
			Stories:     stories,
			StoryShown:  len(stories),
			StoryTotal:  paginator.Total,
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

func dispatchStoryList(ctx context.Context, projectID, cursor string, limit int) ([]storyRow, string, error) {
	// Post-unification (sty_0dd71f79) stories are documents-of-type-story.
	// Server-side cursor pagination (sty_5c41f316) — Limit is the page
	// size, Cursor is the opaque token document_list emits.
	body, _ := json.Marshal(verb.DocumentListRequest{
		Type:      "story",
		ProjectID: projectID,
		Limit:     limit,
		Cursor:    cursor,
	})
	raw, err := verb.Dispatch(ctx, "document_list", body)
	if err != nil {
		return nil, "", err
	}
	var resp verb.DocumentListResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, "", err
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
	return out, resp.NextCursor, nil
}

// dispatchStoryCount returns the total story count for the project
// (path B per sty_975afe24 — count ignores the panel's client-side
// chip filter). Failures degrade silently; the handler falls back to
// 0 which the template renders as "page N" without an "of M".
func dispatchStoryCount(ctx context.Context, projectID string) int {
	body, _ := json.Marshal(verb.DocumentCountRequest{
		Type:      "story",
		ProjectID: projectID,
	})
	raw, err := verb.Dispatch(ctx, "document_count", body)
	if err != nil {
		arbor.WarnCtx(ctx, "story count: dispatch failed, total unavailable", "err", err)
		return 0
	}
	var resp verb.DocumentCountResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		arbor.WarnCtx(ctx, "story count: decode failed, total unavailable", "err", err)
		return 0
	}
	return resp.Count
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

// storyPageSizeFallback is used when the system KV lookup misses or
// returns a non-numeric value. storyPageSizeMax aligns with
// document_list's own Limit ceiling so we never exceed what the verb
// will return in a single fetch.
const (
	storyPageSizeFallback = 50
	storyPageSizeMax      = 200
)

// readStoryPageSize fetches the operator-tuned page size from the
// system KV (sty_cc4c671a) via variable_get. Falls back to the const on
// miss / non-numeric value / verb error with a WARN log so the
// misconfiguration is visible without breaking the panel.
func readStoryPageSize(ctx context.Context) int {
	body, _ := json.Marshal(verb.VariableGetRequest{
		Name:  "stories.page_size",
		Scope: "system",
	})
	raw, err := verb.Dispatch(ctx, "variable_get", body)
	if err != nil {
		arbor.WarnCtx(ctx, "story page_size: variable_get failed, using fallback",
			"fallback", storyPageSizeFallback, "err", err)
		return storyPageSizeFallback
	}
	var resp verb.VariableGetResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		arbor.WarnCtx(ctx, "story page_size: decode failed, using fallback", "err", err)
		return storyPageSizeFallback
	}
	n, err := strconv.Atoi(strings.TrimSpace(resp.Value))
	if err != nil || n < 1 {
		arbor.WarnCtx(ctx, "story page_size: non-numeric KV value, using fallback",
			"value", resp.Value, "fallback", storyPageSizeFallback)
		return storyPageSizeFallback
	}
	if n > storyPageSizeMax {
		return storyPageSizeMax
	}
	return n
}

// emptyCursorSentinel encodes "no cursor" inside the comma-separated
// stories_back URL param. Cursors are base64; the underscore is not
// part of the base64 alphabet, so a sentinel of "_" doesn't collide
// with a real cursor value.
const emptyCursorSentinel = "_"

// readBackStack pulls the comma-separated list of cursors we came from
// out of the URL. Each entry is the document_list cursor that produced
// the page at that depth. The first page's "cursor" is the empty
// string, encoded as emptyCursorSentinel in the URL so it survives the
// comma-split.
func readBackStack(q url.Values) []string {
	s := q.Get("stories_back")
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, len(parts))
	for i, p := range parts {
		if p == emptyCursorSentinel {
			out[i] = ""
		} else {
			out[i] = p
		}
	}
	return out
}

// encodeBackStack inverts readBackStack — replaces empty cursors with
// the sentinel so the joined URL value carries N entries unambiguously.
func encodeBackStack(stack []string) string {
	parts := make([]string, len(stack))
	for i, c := range stack {
		if c == "" {
			parts[i] = emptyCursorSentinel
		} else {
			parts[i] = c
		}
	}
	return strings.Join(parts, ",")
}

// nextPageURL pushes the current cursor onto the back stack and uses
// nextCursor as the new cursor — the link the operator clicks to go
// forward.
func nextPageURL(base string, currentPage int, currentCursor, nextCursor string, backStack []string) string {
	newBack := append([]string(nil), backStack...)
	newBack = append(newBack, currentCursor)
	v := url.Values{}
	v.Set("stories_page", strconv.Itoa(currentPage+1))
	v.Set("stories_cursor", nextCursor)
	v.Set("stories_back", encodeBackStack(newBack))
	return base + "?" + v.Encode()
}

// prevPageURL pops the top of the back stack to get the previous
// page's cursor. The back stack shrinks by one entry.
func prevPageURL(base string, currentPage int, backStack []string) string {
	if len(backStack) == 0 {
		return base
	}
	prevCursor := backStack[len(backStack)-1]
	newBack := backStack[:len(backStack)-1]
	v := url.Values{}
	if currentPage-1 > 1 {
		v.Set("stories_page", strconv.Itoa(currentPage-1))
	}
	if prevCursor != "" {
		v.Set("stories_cursor", prevCursor)
	}
	if len(newBack) > 0 {
		v.Set("stories_back", encodeBackStack(newBack))
	}
	enc := v.Encode()
	if enc == "" {
		return base
	}
	return base + "?" + enc
}
