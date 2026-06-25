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
	"github.com/bobmcallan/satellites/internal/workflow"
)

var projectDetailTmpl = template.Must(
	template.New("project_detail.html").Funcs(template.FuncMap{
		"formatTime": formatRowTime,
	}).ParseFS(assets, "templates/project_detail.html", "templates/_user_menu.html"),
)

type projectDetailData struct {
	Title      string
	UserEmail  string
	UserName   string
	UserAvatar string
	ActiveNav  string
	Project    projectRow
	Stories    []storyRow
	// Tasks (type:task) for the read-only tasks panel, peer to the stories list
	// (epic:workflow-steps order-6). Read-only — surfaces existing data only.
	Tasks        []taskRow
	TaskFiltered int // tasks matching the active filter (indicator numerator, sty_80447ada)
	TaskTotal    int // project-wide task total (indicator denominator)
	// Documents (type:document) for the project Documents panel — peer to the
	// stories/tasks lists (epic:phases-task-outputs). Filterable + inline preview.
	Documents   []docRow
	DocFiltered int    // documents matching the active filter (indicator numerator)
	DocTotal    int    // project-wide document total (indicator denominator)
	DocQuery    string // the active docs_q (rehydrates the search box on reload)
	// CodegraphHTML is the newest published type:codegraph document rendered as safe
	// markdown (epic:codegraph-usability B2). Empty when the project has none — the
	// template then renders no codegraph card (graceful absence). codegraph-init.js
	// turns the embedded language-codegraph block into an interactive graph.
	CodegraphHTML template.HTML
	// CodegraphTable is the package table derived server-side from the same canonical
	// codegraph JSON block — the no-JS-readable view peer to the client-side interactive
	// diagram. Empty when the body carries no parseable block.
	CodegraphTable template.HTML
	// Codegraph format gate (epic:codegraph-portable): a published type:codegraph document
	// declares its format via a format:<id> tag, and the portal renders the diagram + table
	// ONLY when that format is acceptable. CodegraphFound = a document exists;
	// CodegraphAcceptable = its format is one the UI can render; CodegraphFormat = the
	// declared id (shown in the graceful "not rendered here" notice for other formats —
	// the document is still agent-readable over MCP).
	CodegraphFound      bool
	CodegraphAcceptable bool
	CodegraphFormat     string
	StoryShown          int // rows rendered on this page = len(Stories)
	StoryFiltered       int // stories matching the active filter across all pages (indicator numerator)
	StoryTotal          int // project-wide all-status total (indicator denominator)
	Paginator           paginatorData
	// Engagement-age thresholds (seconds) for the processing dot — rendered
	// as container data attributes; the client computes colors (sty_25e2e8ac).
	EngOrangeSecs int
	EngRedSecs    int
	DevMode       bool
	FooterName    string
	FooterEmail   string
	Version       string
}

// paginatorData carries the paginator state the template renders.
type paginatorData struct {
	Page      int
	PageCount int // pages over the FILTERED set; 0 = unknown
	Filtered  int // stories matching the active filter (numerator + page basis)
	Total     int // project-wide all-status count (indicator denominator)
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
	// Rendered-markdown projection for the readable expanded panel
	// (sty_762730ad) — the same safe goldmark renderer the changelog uses.
	// ACs are part of the body, so there is no separate AC projection
	// (sty_bf2fc8e1).
	BodyHTML template.HTML
	Status   string
	// StatusRank is the index of Status within the story's OWN ## Workflow
	// states (lifecycle order), or statusRankUnknown when the story carries no
	// workflow block or its status is not one of the declared states. The portal
	// sorts `order:status` by this rank so a custom lifecycle (integration_review,
	// commit, …) orders by its real position rather than alphabetically
	// (epic:dynamic-workflow-status order:3). Workflow-derived, never a hardcoded
	// status enum.
	StatusRank int
	Priority   string
	Category   string
	Tags       []string
	UpdatedAt  time.Time
	CreatedAt  time.Time
	// Engagement signal (sty_25e2e8ac): newest engagement row's last_seen /
	// lease_until for this story (RFC3339, empty = no engagement). The portal
	// renders a processing dot whose color is computed client-side from age —
	// the server pushes no timeout state.
	EngLastSeen   string
	EngLeaseUntil string
	// Reviews is the story's review-history light strip (sty_14f07f22): one dot
	// per reviewer-gate verdict (pass/fail) in order, plus a trailing pending dot
	// for an unresolved request. Distinct from the engagement freshness dot.
	Reviews []reviewLight
}

// statusRankUnknown sorts unworkflowed / unknown-status rows to the end of a
// status sort. Larger than any realistic workflow length.
const statusRankUnknown = 1 << 30

// statusRank resolves a row's workflow-ordered status position from its body's
// ## Workflow block (internal/workflow is the single parser, also used by the
// story-detail view). A missing/unparseable workflow or an off-workflow status
// yields statusRankUnknown — the row sinks rather than guessing an order.
func statusRank(body, status string) int {
	wf, err := workflow.ParseBody([]byte(body))
	if err != nil {
		return statusRankUnknown
	}
	for i, s := range wf.States {
		if s.Name == status {
			return i
		}
	}
	return statusRankUnknown
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

		stories, paginator, err := gatherStoryPage(ctx, projectID, r.URL.Query())
		if err != nil {
			arbor.ErrorCtx(ctx, "project_detail: gather stories", "id", projectID, "err", err)
			http.Error(w, "could not list stories", http.StatusInternalServerError)
			return
		}

		// Tasks panel (epic:workflow-steps order-6; search/count/status-filter
		// sty_80447ada) — best-effort: a task-list error must not blank the whole
		// project page (stories still render). The panel applies the same query
		// grammar as stories via gatherTaskPanel (tasks_q).
		tasks, taskFiltered, taskTotal, tErr := gatherTaskPanel(ctx, projectID, r.URL.Query())
		if tErr != nil {
			arbor.WarnCtx(ctx, "project_detail: list tasks", "id", projectID, "err", tErr)
		}

		// Documents panel (epic:phases-task-outputs) — best-effort, peer to tasks:
		// a documents-list error must not blank the whole project page.
		docs, docFiltered, docTotal, dErr := gatherDocPanel(ctx, projectID, r.URL.Query())
		if dErr != nil {
			arbor.WarnCtx(ctx, "project_detail: list documents", "id", projectID, "err", dErr)
		}

		// Codegraph card (epic:codegraph-usability B2) — best-effort, peer to the
		// documents panel: surfaces the project's published type:codegraph document so
		// codegraph-init.js can render it interactively. Absent → no card.
		cg := gatherCodegraph(ctx, projectID)

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
				ID: pj.ID, Name: pj.Name, Description: pj.Description, Type: pj.Type,
				GitURL: pj.GitURLCanonical, Status: pj.Status, CreatedAt: pj.CreatedAt,
			},
			UserEmail:           userEmail,
			UserName:            userName,
			UserAvatar:          userAvatar,
			ActiveNav:           "projects",
			Stories:             stories,
			Tasks:               tasks,
			TaskFiltered:        taskFiltered,
			TaskTotal:           taskTotal,
			Documents:           docs,
			DocFiltered:         docFiltered,
			DocTotal:            docTotal,
			DocQuery:            strings.TrimSpace(r.URL.Query().Get("docs_q")),
			CodegraphHTML:       cg.Body,
			CodegraphTable:      cg.Table,
			CodegraphFound:      cg.Found,
			CodegraphFormat:     cg.Format,
			CodegraphAcceptable: cg.Acceptable,
			StoryShown:          len(stories),
			StoryFiltered:       paginator.Filtered,
			StoryTotal:          paginator.Total,
			Paginator:           paginator,
			EngOrangeSecs:       readEngagementThreshold(ctx, "engagement.orange_after_secs", engOrangeSecsFallback),
			EngRedSecs:          readEngagementThreshold(ctx, "engagement.red_after_secs", engRedSecsFallback),
			DevMode:             cfg.DevMode,
			FooterName:          footerName,
			FooterEmail:         footerEmail,
			Version:             versionString(),
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := projectDetailTmpl.Execute(w, data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// gatherStoryPage resolves the story slice + paginator for a project given the
// request query (filter + cursor/page params), including the per-story body
// fetch for the visible slice. Shared by the full-page handler and the live
// fragment endpoint (sty_8f69be8b) so both render the identical page.
func gatherStoryPage(ctx context.Context, projectID string, q url.Values) ([]storyRow, paginatorData, error) {
	pageSize := readStoryPageSize(ctx)
	page := 1
	if p := strings.TrimSpace(q.Get("stories_page")); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			page = n
		}
	}

	// All filtering + paging is server-side (sty_47234d6e): gather the whole
	// project story set, apply the EFFECTIVE query, sort, then offset-paginate.
	// An empty stories_q is the default view = `status:open` — applied here, not
	// client-side, so each page is a full page of matching rows and the count
	// reflects the filter (was: client hid done/cancelled over a server page,
	// holing the pages). `filtered` is the match count across all pages; `total`
	// is the project-wide all-status count.
	base := "/projects/" + projectID
	qparam := strings.TrimSpace(q.Get("stories_q"))
	sq := parseStoryQuery(qparam)
	if sq.isEmpty() {
		sq = parseStoryQuery("status:open")
	}

	all, err := gatherAllStories(ctx, projectID, pageSize)
	if err != nil {
		return nil, paginatorData{}, err
	}
	grandTotal := len(all)
	filtered := filterStories(all, sq)
	sortStories(filtered, sq.order)
	filteredCount := len(filtered)

	start := (page - 1) * pageSize
	if start > filteredCount {
		start = filteredCount
	}
	end := start + pageSize
	if end > filteredCount {
		end = filteredCount
	}
	stories := filtered[start:end]

	paginator := paginatorData{
		Page: page, Filtered: filteredCount, Total: grandTotal, PageSize: pageSize,
		HasPrev: page > 1, HasNext: end < filteredCount,
	}
	if pageSize > 0 {
		paginator.PageCount = (filteredCount + pageSize - 1) / pageSize
		if paginator.PageCount < 1 {
			paginator.PageCount = 1
		}
	}
	if paginator.HasPrev {
		paginator.PrevURL = filteredPageURL(base, qparam, page-1)
	}
	if paginator.HasNext {
		paginator.NextURL = filteredPageURL(base, qparam, page+1)
	}

	// Per-story body fetch so the expanded row can render the description.
	// Render the body + ACs as markdown for the readable expanded panel
	// (sty_762730ad).
	for i := range stories {
		body, err := dispatchStoryBody(ctx, stories[i].ID)
		if err != nil {
			arbor.WarnCtx(ctx, "project_detail: story_body", "story_id", stories[i].ID, "err", err)
		} else {
			stories[i].Body = body
		}
		if strings.TrimSpace(stories[i].Body) != "" {
			stories[i].BodyHTML = renderStoryMarkdown(stories[i].Body)
		}
		// Workflow-ordered status rank for `order:status` (order:3). The body is
		// now loaded, so the row's own ## Workflow is parseable here.
		stories[i].StatusRank = statusRank(stories[i].Body, stories[i].Status)
	}
	attachEngagements(ctx, projectID, stories)
	attachReviewLights(ctx, projectID, stories)
	return stories, paginator, nil
}

// attachEngagements decorates the visible rows with the newest engagement
// signal per story, folded across sessions (sty_25e2e8ac). Best-effort: a
// failure leaves the rows undecorated (no dot) rather than failing the page.
func attachEngagements(ctx context.Context, projectID string, stories []storyRow) {
	views, err := latestEngagements(ctx, projectID, time.Now().UTC())
	if err != nil {
		arbor.WarnCtx(ctx, "project_detail: engagements", "id", projectID, "err", err)
		return
	}
	newest := map[string]engagementView{}
	for _, v := range views {
		if cur, ok := newest[v.StoryID]; !ok || v.LastSeen.After(cur.LastSeen) {
			newest[v.StoryID] = v
		}
	}
	for i := range stories {
		if !engagementVisibleStatus(stories[i].Status) {
			continue // backlog / terminal rows never wear the indicator
		}
		if v, ok := newest[stories[i].ID]; ok {
			stories[i].EngLastSeen = v.LastSeen.UTC().Format(time.RFC3339)
			stories[i].EngLeaseUntil = v.LeaseUntil
		}
	}
}

// engagementVisibleStatus gates the activity indicator by row status
// (sty_07bb85b6): a story not yet started (backlog) or finished
// (done / cancelled) shows no indicator, whatever engagement rows linger.
func engagementVisibleStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "backlog", "done", "cancelled", "canceled", "":
		return false
	}
	return true
}

// Engagement-age thresholds (seconds) — system key/values with fallbacks, so
// retuning the portal indicator needs no code change (sty_25e2e8ac AC4).
const (
	engOrangeSecsFallback = 300
	engRedSecsFallback    = 900
)

// readEngagementThreshold mirrors readStoryPageSize: a system-scope variable
// read that falls back on any failure.
func readEngagementThreshold(ctx context.Context, name string, fallback int) int {
	body, _ := json.Marshal(verb.VariableGetRequest{Name: name, Scope: "system"})
	raw, err := verb.Dispatch(ctx, "variable_get", body)
	if err != nil {
		return fallback
	}
	var resp verb.VariableGetResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fallback
	}
	n, err := strconv.Atoi(strings.TrimSpace(resp.Value))
	if err != nil || n < 1 {
		return fallback
	}
	return n
}

// storyFragmentHandler renders just the story-rows table fragment for the
// current query — the live-update refetch target (sty_8f69be8b). The browser
// swaps it into the panel's <tbody> on a project-scoped SSE trigger; counts
// ride response headers so the indicator updates without a full reload. Same
// read verbs as the full page; no new MCP verb.
func storyFragmentHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := cfg.Sessions.UserID(r)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := withSessionUser(r.Context(), cfg, userID)
		projectID := strings.TrimSpace(r.PathValue("id"))
		if projectID == "" {
			http.NotFound(w, r)
			return
		}
		stories, paginator, err := gatherStoryPage(ctx, projectID, r.URL.Query())
		if err != nil {
			arbor.ErrorCtx(ctx, "story_fragment: gather", "id", projectID, "err", err)
			http.Error(w, "could not list stories", http.StatusInternalServerError)
			return
		}
		w.Header().Set("X-Story-Filtered", strconv.Itoa(paginator.Filtered))
		w.Header().Set("X-Story-Total", strconv.Itoa(paginator.Total))
		w.Header().Set("X-Story-Page", strconv.Itoa(paginator.Page))
		w.Header().Set("X-Story-Page-Count", strconv.Itoa(paginator.PageCount))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		data := projectDetailData{
			Stories: stories, Paginator: paginator,
			StoryFiltered: paginator.Filtered, StoryTotal: paginator.Total, StoryShown: len(stories),
		}
		if err := projectDetailTmpl.ExecuteTemplate(w, "story-rows", data); err != nil {
			arbor.ErrorCtx(ctx, "story_fragment: render", "id", projectID, "err", err)
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
	Type            string    `json:"type"`
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

// gatherAllStories pages through document_list (cursor) to collect every
// story in the project, so the server-side filter (sty_1bd0098a) can reach
// matches on any page. Bounded by a page cap so a runaway never loops
// forever; hitting it WARNs that the filter may be incomplete.
func gatherAllStories(ctx context.Context, projectID string, pageSize int) ([]storyRow, error) {
	const maxPages = 200 // 200 × pageSize rows is far beyond any real project
	var all []storyRow
	cursor := ""
	for i := 0; i < maxPages; i++ {
		rows, next, err := dispatchStoryList(ctx, projectID, cursor, pageSize)
		if err != nil {
			return nil, err
		}
		all = append(all, rows...)
		if next == "" {
			return all, nil
		}
		cursor = next
	}
	arbor.WarnCtx(ctx, "gatherAllStories: hit page cap, filter may be incomplete",
		"project_id", projectID, "cap_rows", maxPages*pageSize)
	return all, nil
}

// filteredPageURL builds a paginator link: the panel query rides as stories_q
// so prev/next preserve the filter, and stories_page selects the offset page
// (omitted for page 1). An empty query is the default view and is omitted so
// the default URL stays clean (/projects/{id}).
func filteredPageURL(base, query string, page int) string {
	v := url.Values{}
	if query != "" {
		v.Set("stories_q", query)
	}
	if page > 1 {
		v.Set("stories_page", strconv.Itoa(page))
	}
	if enc := v.Encode(); enc != "" {
		return base + "?" + enc
	}
	return base
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
// back-stack URL param. Cursors are base64; the underscore is not
// part of the base64 alphabet, so a sentinel of "_" doesn't collide
// with a real cursor value.
const emptyCursorSentinel = "_"

// paginatorKeys names the URL params a paginator uses. The cursor-stack model
// now serves only the /changelog page (the stories panel moved to server-side
// offset paging in sty_47234d6e); the helpers below stay for changelog.
type paginatorKeys struct {
	Page, Cursor, Back string
}

// readBackStack pulls the comma-separated list of cursors we came from
// out of the URL. Each entry is the cursor that produced the page at
// that depth. The first page's "cursor" is the empty string, encoded
// as emptyCursorSentinel so it survives the comma-split.
func readBackStack(q url.Values, keys paginatorKeys) []string {
	s := q.Get(keys.Back)
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
func nextPageURL(base string, currentPage int, currentCursor, nextCursor string, backStack []string, keys paginatorKeys) string {
	newBack := append([]string(nil), backStack...)
	newBack = append(newBack, currentCursor)
	v := url.Values{}
	v.Set(keys.Page, strconv.Itoa(currentPage+1))
	v.Set(keys.Cursor, nextCursor)
	v.Set(keys.Back, encodeBackStack(newBack))
	return base + "?" + v.Encode()
}

// prevPageURL pops the top of the back stack to get the previous
// page's cursor. The back stack shrinks by one entry.
func prevPageURL(base string, currentPage int, backStack []string, keys paginatorKeys) string {
	if len(backStack) == 0 {
		return base
	}
	prevCursor := backStack[len(backStack)-1]
	newBack := backStack[:len(backStack)-1]
	v := url.Values{}
	if currentPage-1 > 1 {
		v.Set(keys.Page, strconv.Itoa(currentPage-1))
	}
	if prevCursor != "" {
		v.Set(keys.Cursor, prevCursor)
	}
	if len(newBack) > 0 {
		v.Set(keys.Back, encodeBackStack(newBack))
	}
	enc := v.Encode()
	if enc == "" {
		return base
	}
	return base + "?" + enc
}
