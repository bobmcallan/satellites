// Portal /ledger page (sty_becb7019). Renders evidence ledger rows
// filtered by a single search box (key:value tokens + free-text body
// terms) plus a created_at window (default last 24h). The URL carries
// the parsed filter so deep links are shareable; pagination uses the
// existing ledger_list cursor with a load-more button.
//
// Transport layering: this file does NOT import internal/ledger.
// pr_mcp_cli_shared_path enforces that transports go through
// internal/verb's Dispatch; the rendered field names below mirror the
// ledger.Entry JSON shape without naming the type directly.

package server

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/verb"
)

var ledgerTmpl = template.Must(template.ParseFS(assets,
	"templates/ledger.html", "templates/_user_menu.html"))

const (
	ledgerPageSize          = 100
	ledgerDefaultWindowHrs  = 24
	ledgerCreatedFormatHTML = "2006-01-02T15:04" // <input type="datetime-local">
	ledgerLogKindPrefix     = "log:"
)

// ledgerListInput is the parsed-search form of the ledger filter,
// expressed in the verb's wire shape so dispatchLedgerList can pass
// it straight through.
type ledgerListInput struct {
	ProjectID       string
	WorkspaceID     string
	SessionID       string
	StoryID         string
	RunID           string
	Kind            string
	BodyContainsAny []string
	CreatedAfter    time.Time
	CreatedBefore   time.Time
	Cursor          string
}

func (in ledgerListInput) isEmpty() bool {
	return in.ProjectID == "" && in.WorkspaceID == "" && in.SessionID == "" &&
		in.StoryID == "" && in.RunID == "" && in.Kind == "" &&
		len(in.BodyContainsAny) == 0
}

type ledgerFilterView struct {
	Search        string // raw search-box value, echoed back into the input
	CreatedAfter  string // value to put back into <input type="datetime-local">
	CreatedBefore string
}

type ledgerEntryView struct {
	ID          string
	Timestamp   string
	Kind        string
	Level       string
	Source      string
	RunID       string
	StoryID     string
	SessionID   string
	ProjectID   string
	WorkspaceID string
	Actor       string
	BodyTrunc   string
	BodyFull    string
	PayloadJSON template.HTML
	RefsJSON    template.HTML
	HasExpand   bool
}

type ledgerPageData struct {
	Title       string
	Filter      ledgerFilterView
	Entries     []ledgerEntryView
	HasNext     bool
	NextURL     string
	Empty       bool
	ErrorMsg    string
	UserEmail   string
	UserName    string
	UserAvatar  string
	ActiveNav   string
	FooterName  string
	FooterEmail string
	Version     string
}

func ledgerHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ledger" {
			http.NotFound(w, r)
			return
		}
		userID, err := cfg.Sessions.UserID(r)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		ctx := withSessionUser(r.Context(), cfg, userID)

		q := r.URL.Query()
		searchRaw := q.Get("q")
		filter := parseLedgerSearch(searchRaw)
		filter.Cursor = q.Get("cursor")

		filterView := ledgerFilterView{Search: searchRaw}
		if s := strings.TrimSpace(q.Get("created_after")); s != "" {
			if t, ok := parseFlexibleTime(s); ok {
				filter.CreatedAfter = t
				filterView.CreatedAfter = t.Format(ledgerCreatedFormatHTML)
			}
		}
		if s := strings.TrimSpace(q.Get("created_before")); s != "" {
			if t, ok := parseFlexibleTime(s); ok {
				filter.CreatedBefore = t
				filterView.CreatedBefore = t.Format(ledgerCreatedFormatHTML)
			}
		}

		data := ledgerPageData{
			Title:       "ledger · satellites",
			Filter:      filterView,
			ActiveNav:   "ledger",
			FooterName:  footerName,
			FooterEmail: footerEmail,
			Version:     versionString(),
		}
		if cfg.Store != nil && cfg.Store.DB != nil {
			if u, err := cfg.Store.GetUserByID(ctx, userID); err == nil && u != nil {
				data.UserEmail = u.Email
				data.UserName = u.DisplayName
				data.UserAvatar = avatarLetter(u.DisplayName, u.Email)
			}
		}

		// Default the time window to last 24h when the operator has
		// supplied no filters at all — otherwise the table is bounded by
		// whatever they typed.
		if filter.isEmpty() && filter.CreatedAfter.IsZero() && filter.CreatedBefore.IsZero() {
			filter.CreatedAfter = time.Now().Add(-ledgerDefaultWindowHrs * time.Hour).UTC()
			data.Filter.CreatedAfter = filter.CreatedAfter.Format(ledgerCreatedFormatHTML)
		}

		resp, err := dispatchLedgerList(ctx, filter)
		if err != nil {
			arbor.WarnCtx(ctx, "ledger page: ledger_list", "err", err)
			data.ErrorMsg = err.Error()
		} else {
			data.Entries = renderLedgerEntries(resp.Entries)
			data.Empty = len(data.Entries) == 0
			if resp.NextCursor != "" {
				data.HasNext = true
				data.NextURL = buildLedgerURL(q, resp.NextCursor)
			}
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := ledgerTmpl.Execute(w, data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// ledgerSearchKeys is the set of `key:value` tokens the search input
// recognises as structured filters. Tokens whose key is unknown fall
// through to the free-text body-search bucket so a typo doesn't
// silently match nothing.
var ledgerSearchKeys = map[string]func(in *ledgerListInput, v string){
	"project_id":   func(in *ledgerListInput, v string) { in.ProjectID = v },
	"workspace_id": func(in *ledgerListInput, v string) { in.WorkspaceID = v },
	"session_id":   func(in *ledgerListInput, v string) { in.SessionID = v },
	"story_id":     func(in *ledgerListInput, v string) { in.StoryID = v },
	"run_id":       func(in *ledgerListInput, v string) { in.RunID = v },
	"kind":         func(in *ledgerListInput, v string) { in.Kind = v },
}

// parseLedgerSearch splits the search-box value into structured
// filters and a free-text body-search bucket.
//
// Tokens are whitespace-separated. A token of shape `<key>:<value>`
// where key is in ledgerSearchKeys becomes the corresponding filter.
// Any other token (including unknown-key forms) joins the body-search
// list — the store ORs those together so `Principle initialisation`
// matches rows whose body contains either word.
func parseLedgerSearch(q string) ledgerListInput {
	in := ledgerListInput{}
	for _, tok := range strings.Fields(q) {
		if idx := strings.Index(tok, ":"); idx > 0 {
			k := strings.ToLower(tok[:idx])
			v := tok[idx+1:]
			if setter, ok := ledgerSearchKeys[k]; ok {
				setter(&in, v)
				continue
			}
		}
		in.BodyContainsAny = append(in.BodyContainsAny, tok)
	}
	return in
}

// parseFlexibleTime accepts either an HTML <input type="datetime-local">
// value (YYYY-MM-DDTHH:MM) or a full RFC3339 timestamp. The HTML form
// uses the short form; URLs operators paste from logs or other tools
// may carry timezone offsets.
func parseFlexibleTime(s string) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), true
	}
	if t, err := time.Parse(ledgerCreatedFormatHTML, s); err == nil {
		return t.UTC(), true
	}
	return time.Time{}, false
}

func dispatchLedgerList(ctx context.Context, in ledgerListInput) (verb.LedgerListResponse, error) {
	req := verb.LedgerListRequest{
		ProjectID:       in.ProjectID,
		WorkspaceID:     in.WorkspaceID,
		SessionID:       in.SessionID,
		StoryID:         in.StoryID,
		RunID:           in.RunID,
		Kind:            in.Kind,
		BodyContainsAny: in.BodyContainsAny,
		Limit:           ledgerPageSize,
		Cursor:          in.Cursor,
	}
	if !in.CreatedAfter.IsZero() {
		req.CreatedAfter = in.CreatedAfter.UTC().Format(time.RFC3339)
	}
	if !in.CreatedBefore.IsZero() {
		req.CreatedBefore = in.CreatedBefore.UTC().Format(time.RFC3339)
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return verb.LedgerListResponse{}, err
	}
	out, err := verb.Dispatch(ctx, "ledger_list", raw)
	if err != nil {
		return verb.LedgerListResponse{}, err
	}
	var resp verb.LedgerListResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return verb.LedgerListResponse{}, err
	}
	return resp, nil
}

// renderLedgerEntries shapes verb-response rows for the template.
// Long bodies are truncated for the table; the full body and the
// JSON-pretty payload/refs ride along for the row's expand panel.
func renderLedgerEntries(entries any) []ledgerEntryView {
	out := []ledgerEntryView{}
	b, err := json.Marshal(entries)
	if err != nil {
		return out
	}
	var rows []ledgerEntryJSON
	if err := json.Unmarshal(b, &rows); err != nil {
		return out
	}
	for _, e := range rows {
		v := ledgerEntryView{
			ID:          e.ID,
			Timestamp:   e.CreatedAt.UTC().Format("2006-01-02 15:04:05Z"),
			Kind:        e.Kind,
			Level:       deriveLevel(e.Kind),
			RunID:       e.RunID,
			StoryID:     e.StoryID,
			SessionID:   e.SessionID,
			ProjectID:   e.ProjectID,
			WorkspaceID: e.WorkspaceID,
			Actor:       e.Actor,
			BodyFull:    e.Body,
			BodyTrunc:   truncateBody(e.Body, 160),
		}
		switch {
		case e.RunID != "":
			v.Source = "run " + e.RunID
		case e.StoryID != "":
			v.Source = "story " + e.StoryID
		case e.SessionID != "":
			v.Source = "session " + e.SessionID
		case e.ProjectID != "":
			v.Source = "project " + e.ProjectID
		case e.WorkspaceID != "":
			v.Source = "workspace " + e.WorkspaceID
		}
		if pj, ok := prettyJSON(e.Payload); ok {
			v.PayloadJSON = template.HTML(pj)
			v.HasExpand = true
		}
		if rj, ok := prettyJSON(e.Refs); ok && rj != "[]" {
			v.RefsJSON = template.HTML(rj)
			v.HasExpand = true
		}
		out = append(out, v)
	}
	return out
}

// ledgerEntryJSON mirrors the JSON shape of ledger.Entry that the
// ledger_list verb returns. Kept here (rather than imported) to honour
// the transport-layer import ban on internal/ledger.
type ledgerEntryJSON struct {
	ID          string          `json:"id"`
	StoryID     string          `json:"story_id"`
	ProjectID   string          `json:"project_id"`
	WorkspaceID string          `json:"workspace_id"`
	SessionID   string          `json:"session_id"`
	RunID       string          `json:"run_id"`
	Kind        string          `json:"kind"`
	Actor       string          `json:"actor"`
	Body        string          `json:"body"`
	Payload     json.RawMessage `json:"payload"`
	Refs        json.RawMessage `json:"refs"`
	CreatedAt   time.Time       `json:"created_at"`
}

func deriveLevel(kind string) string {
	if strings.HasPrefix(kind, ledgerLogKindPrefix) {
		return strings.TrimPrefix(kind, ledgerLogKindPrefix)
	}
	return ""
}

func truncateBody(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func prettyJSON(raw []byte) (string, bool) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "{}" || s == "null" {
		return "", false
	}
	var v any
	if err := json.Unmarshal(raw, &v); err == nil {
		if b, err := json.MarshalIndent(v, "", "  "); err == nil {
			return string(b), true
		}
	}
	return s, true
}

func buildLedgerURL(q url.Values, cursor string) string {
	v := url.Values{}
	for _, k := range []string{"q", "created_after", "created_before"} {
		if s := strings.TrimSpace(q.Get(k)); s != "" {
			v.Set(k, s)
		}
	}
	v.Set("cursor", cursor)
	return "/ledger?" + v.Encode()
}
