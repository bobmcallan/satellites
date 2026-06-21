// task_detail.go — the read-only operator views for tasks (type:task) and their
// execution episodes (epic:workflow-steps order-6). The tasks panel now expands
// INLINE on the project page like story rows (sty_f46fe4f2): a row reveals the
// task content (safe markdown) + its ledger/log grouped BY EXECUTION episode.
// These views surface EXISTING data only — document_get / document_list /
// ledger_list read verbs — and never write a ledger row or patch a status
// (reviewer-only stays intact). Episodes are grouped by internal/taskepisode,
// the SAME projection `satellites task executions` uses. /tasks/{id} deep-links
// the project page with that task's row expanded (mirroring /stories/{id}).
package server

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/taskepisode"
	"github.com/bobmcallan/satellites/internal/verb"
)

// taskLedgerPageLimit bounds one ledger_list page; the panel pages the full
// ledger so every episode (across many runs) is grouped.
const taskLedgerPageLimit = 200

// taskRow is one task in the project-detail tasks panel — peer to storyRow. It
// carries the inline-expand payload (BodyHTML + Episodes) so the row reveals the
// task content + per-execution log without leaving the project page.
type taskRow struct {
	ID       string
	Title    string
	Status   string
	Priority string
	Category string // rendered as its own chip, peer to storyRow.Category
	Tags     []string
	RunCount int               // execution episodes derived from the ledger
	LastRun  string            // formatted start of the most recent episode ("" = never run)
	BodyHTML template.HTML     // task body rendered as safe markdown (inline expand)
	Episodes []taskEpisodeView // ledger/log grouped by execution episode (inline expand)
}

// taskEpisodeView is one execution episode for the inline expand: the projected
// run metadata plus the ledger/log rows that fall inside it.
type taskEpisodeView struct {
	Run    int
	Start  string
	End    string
	Status string // terminal status, or "running (open)"
	Open   bool
	Rows   []taskLedgerRowView
}

// taskLedgerRowView is one ledger row rendered in an episode.
type taskLedgerRowView struct {
	When       string
	Kind       string
	Actor      string
	Body       string
	Transition string // "→ to" for a status_transition ("" otherwise)
}

// taskDoc is the slice of a task document the panel + deep-link redirect need.
type taskDoc struct {
	ID        string
	Name      string
	Status    string
	ProjectID string
	RawBody   string
}

// dispatchTaskGet fetches one task document (metadata + raw body). Read-only.
func dispatchTaskGet(ctx context.Context, taskID string) (taskDoc, error) {
	getReq, _ := json.Marshal(verb.DocumentGetRequest{ID: taskID})
	raw, err := verb.Dispatch(ctx, "document_get", getReq)
	if err != nil {
		return taskDoc{}, err
	}
	var got struct {
		Document struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Status    string `json:"status"`
			ProjectID string `json:"project_id"`
		} `json:"document"`
		RawBody string `json:"raw_body"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		return taskDoc{}, err
	}
	return taskDoc{
		ID:        got.Document.ID,
		Name:      got.Document.Name,
		Status:    got.Document.Status,
		ProjectID: got.Document.ProjectID,
		RawBody:   got.RawBody,
	}, nil
}

// dispatchTaskList lists a project's tasks (document_list type:task) for the
// project-detail panel, building each row's inline-expand payload (body +
// per-execution episodes). Read-only.
func dispatchTaskList(ctx context.Context, projectID string) ([]taskRow, error) {
	body, _ := json.Marshal(verb.DocumentListRequest{Type: "task", ProjectID: projectID, Limit: 200})
	raw, err := verb.Dispatch(ctx, "document_list", body)
	if err != nil {
		return nil, err
	}
	var resp verb.DocumentListResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	out := make([]taskRow, 0, len(resp.Items))
	for _, d := range resp.Items {
		row := taskRow{ID: d.ID, Title: d.Name, Status: d.Status, Priority: d.Priority, Category: d.Category, Tags: d.Tags}
		// Inline-expand content (sty_f46fe4f2): the task body rendered as safe
		// markdown — best-effort; a get error leaves the body empty rather than
		// failing the page.
		if doc, gErr := dispatchTaskGet(ctx, d.ID); gErr != nil {
			arbor.WarnCtx(ctx, "task_list: body", "id", d.ID, "err", gErr)
		} else if strings.TrimSpace(doc.RawBody) != "" {
			row.BodyHTML = renderMarkdown(doc.RawBody)
		}
		// Inline-expand log + run summary: episodes grouped by execution from the
		// full ledger — best-effort; a ledger read error leaves them empty.
		if entries, lErr := dispatchTaskLedger(ctx, d.ID); lErr != nil {
			arbor.WarnCtx(ctx, "task_list: episodes", "id", d.ID, "err", lErr)
		} else {
			eps := taskepisode.Project(taskEpisodeRows(entries))
			row.RunCount = len(eps)
			if len(eps) > 0 {
				row.LastRun = eps[len(eps)-1].Start.Format("2006-01-02 15:04")
			}
			row.Episodes = taskEpisodeViews(entries, eps)
		}
		out = append(out, row)
	}
	return out, nil
}

// taskLedgerEntry mirrors one ledger_list row into the shape both episode
// projection and the row view need (kind, to_status, actor, body, created).
type taskLedgerEntry struct {
	kind    string
	to      string
	actor   string
	body    string
	created time.Time
}

// dispatchTaskLedger pages the full ledger for a task (oldest-first), so every
// episode across every run is present. Read-only.
func dispatchTaskLedger(ctx context.Context, taskID string) ([]taskLedgerEntry, error) {
	var rows []taskLedgerEntry
	cursor := ""
	for {
		body, _ := json.Marshal(verb.LedgerListRequest{StoryID: taskID, Limit: taskLedgerPageLimit, Cursor: cursor})
		raw, err := verb.Dispatch(ctx, "ledger_list", body)
		if err != nil {
			return nil, err
		}
		var resp verb.LedgerListResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, err
		}
		for _, e := range resp.Entries {
			to := ""
			if e.Kind == "status_transition" {
				var p struct {
					To string `json:"to_status"`
				}
				_ = json.Unmarshal(e.Payload, &p)
				to = p.To
			}
			rows = append(rows, taskLedgerEntry{kind: e.Kind, to: to, actor: e.Actor, body: e.Body, created: e.CreatedAt})
		}
		if resp.NextCursor == "" {
			break
		}
		cursor = resp.NextCursor
	}
	return rows, nil
}

// taskEpisodeRows maps ledger entries 1:1 into the projection input rows.
func taskEpisodeRows(entries []taskLedgerEntry) []taskepisode.Row {
	rows := make([]taskepisode.Row, len(entries))
	for i, e := range entries {
		rows[i] = taskepisode.Row{Kind: e.kind, To: e.to, Created: e.created}
	}
	return rows
}

// taskEpisodeViews shapes already-projected episodes into the per-execution view
// model the inline panel renders. Each episode's entries are exactly
// entries[StartIdx : StartIdx+Rows] (the rows slice is 1:1 with entries), so the
// shared projection slices a task's ledger per episode with no second walk.
func taskEpisodeViews(entries []taskLedgerEntry, eps []taskepisode.Episode) []taskEpisodeView {
	out := make([]taskEpisodeView, 0, len(eps))
	for _, ep := range eps {
		ev := taskEpisodeView{
			Run:    ep.Run,
			Start:  ep.Start.Format("2006-01-02 15:04:05Z"),
			Status: "running (open)",
			Open:   ep.Open(),
		}
		if !ep.Open() {
			ev.Status = ep.EndTo
			ev.End = ep.End.Format("2006-01-02 15:04:05Z")
		}
		for _, e := range entries[ep.StartIdx : ep.StartIdx+ep.Rows] {
			rv := taskLedgerRowView{
				When:  e.created.Format("2006-01-02 15:04:05Z"),
				Kind:  e.kind,
				Actor: e.actor,
				Body:  e.body,
			}
			if e.kind == "status_transition" && e.to != "" {
				rv.Transition = "→ " + e.to
			}
			ev.Rows = append(ev.Rows, rv)
		}
		out = append(out, ev)
	}
	return out
}

// taskDetailHandler deep-links /tasks/{id} to the project page with that task's
// row expanded (?task=<id>), mirroring /stories/{id} → /projects/{id}?story=…
// (sty_f46fe4f2 AC#3). The task view is inline on the project page now — there
// is no standalone task page. Read-only.
func taskDetailHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := cfg.Sessions.UserID(r)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		ctx := withSessionUser(r.Context(), cfg, userID)

		taskID := strings.TrimSpace(path.Base(r.URL.Path))
		if taskID == "" || taskID == "tasks" {
			http.NotFound(w, r)
			return
		}

		doc, err := dispatchTaskGet(ctx, taskID)
		if err != nil || doc.ProjectID == "" {
			arbor.WarnCtx(ctx, "task_detail: resolve", "id", taskID, "err", err)
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r,
			"/projects/"+url.PathEscape(doc.ProjectID)+"?task="+url.QueryEscape(taskID),
			http.StatusFound)
	}
}
