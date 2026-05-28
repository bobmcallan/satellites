// Portal /ledger page (sty_becb7019). Renders evidence ledger rows
// filtered by any subset of project_id / workspace_id / session_id /
// story_id / run_id / kind / body_contains and a created_at window
// (default last 24h). Filters are mirrored to query params so the URL
// is shareable; pagination uses the existing ledger_list cursor with a
// load-more button.
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

// ledgerListInput is the parsed-query form of the ledger filter,
// expressed in the verb's wire shape so dispatchLedgerList can pass
// it straight through.
type ledgerListInput struct {
	ProjectID     string
	WorkspaceID   string
	SessionID     string
	StoryID       string
	RunID         string
	Kind          string
	BodyContains  string
	CreatedAfter  time.Time
	CreatedBefore time.Time
	Cursor        string
}

func (in ledgerListInput) isEmpty() bool {
	return in.ProjectID == "" && in.WorkspaceID == "" && in.SessionID == "" &&
		in.StoryID == "" && in.RunID == "" && in.Kind == "" && in.BodyContains == ""
}

type ledgerFilterView struct {
	ProjectID     string
	WorkspaceID   string
	SessionID     string
	StoryID       string
	RunID         string
	Kind          string
	BodyContains  string
	CreatedAfter  string // value to put back into <input type="datetime-local">
	CreatedBefore string
}

type ledgerEntryView struct {
	ID          string
	Timestamp   string
	Kind        string
	Level       string // derived: log:info → "info", "story_created" → ""
	Source      string // first non-empty of run / story / session id, with a label
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
		filter, vView := parseLedgerFilter(q)
		filter.Cursor = q.Get("cursor")

		data := ledgerPageData{
			Title:       "ledger · satellites",
			Filter:      vView,
			ActiveNav:   "ledger",
			FooterName:  footerName,
			FooterEmail: footerEmail,
			Version:     versionString(),
		}
		// User chip — best-effort, mirrors other pages.
		if cfg.Store != nil && cfg.Store.DB != nil {
			if u, err := cfg.Store.GetUserByID(ctx, userID); err == nil && u != nil {
				data.UserEmail = u.Email
				data.UserName = u.DisplayName
				data.UserAvatar = avatarLetter(u.DisplayName, u.Email)
			}
		}

		// If the operator hasn't named any filter (and no time window was
		// provided), default to the last 24h so the table is bounded.
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

// parseLedgerFilter pulls the structured filter out of the query
// string. The form-view duplicates the filter for the template (which
// renders string values back into the input fields).
func parseLedgerFilter(q url.Values) (ledgerListInput, ledgerFilterView) {
	in := ledgerListInput{
		ProjectID:    strings.TrimSpace(q.Get("project_id")),
		WorkspaceID:  strings.TrimSpace(q.Get("workspace_id")),
		SessionID:    strings.TrimSpace(q.Get("session_id")),
		StoryID:      strings.TrimSpace(q.Get("story_id")),
		RunID:        strings.TrimSpace(q.Get("run_id")),
		Kind:         strings.TrimSpace(q.Get("kind")),
		BodyContains: strings.TrimSpace(q.Get("body_contains")),
	}
	v := ledgerFilterView{
		ProjectID:    in.ProjectID,
		WorkspaceID:  in.WorkspaceID,
		SessionID:    in.SessionID,
		StoryID:      in.StoryID,
		RunID:        in.RunID,
		Kind:         in.Kind,
		BodyContains: in.BodyContains,
	}
	if s := strings.TrimSpace(q.Get("created_after")); s != "" {
		if t, ok := parseFlexibleTime(s); ok {
			in.CreatedAfter = t
			v.CreatedAfter = t.Format(ledgerCreatedFormatHTML)
		}
	}
	if s := strings.TrimSpace(q.Get("created_before")); s != "" {
		if t, ok := parseFlexibleTime(s); ok {
			in.CreatedBefore = t
			v.CreatedBefore = t.Format(ledgerCreatedFormatHTML)
		}
	}
	return in, v
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

// dispatchLedgerList routes the typed filter through the verb layer
// so the same code-path the MCP/HTTP transports use is exercised on
// the portal page.
func dispatchLedgerList(ctx context.Context, in ledgerListInput) (verb.LedgerListResponse, error) {
	req := verb.LedgerListRequest{
		ProjectID:    in.ProjectID,
		WorkspaceID:  in.WorkspaceID,
		SessionID:    in.SessionID,
		StoryID:      in.StoryID,
		RunID:        in.RunID,
		Kind:         in.Kind,
		BodyContains: in.BodyContains,
		Limit:        ledgerPageSize,
		Cursor:       in.Cursor,
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
//
// The input is verb.LedgerListResponse.Entries — the element type is
// ledger.Entry but pr_mcp_cli_shared_path keeps the internal/ledger
// import out of this package, so we range over the slice without
// naming the element type.
func renderLedgerEntries(entries any) []ledgerEntryView {
	// Marshal-roundtrip the entries into a transport-shaped struct local
	// to this package so we never name internal/ledger types directly.
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

// buildLedgerURL preserves every filter param on the current URL and
// substitutes the cursor — used by the "Load more" link.
func buildLedgerURL(q url.Values, cursor string) string {
	v := url.Values{}
	for _, k := range []string{
		"project_id", "workspace_id", "session_id", "story_id",
		"run_id", "kind", "body_contains", "created_after", "created_before",
	} {
		if s := strings.TrimSpace(q.Get(k)); s != "" {
			v.Set(k, s)
		}
	}
	v.Set("cursor", cursor)
	return "/ledger?" + v.Encode()
}
