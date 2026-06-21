// task_detail.go — the read-only operator views for tasks (type:task) and their
// execution episodes (epic:workflow-steps order-6). Stories had a portal panel +
// detail + ledger view; tasks had none, so the operator was blind to a task's
// runs. These views surface EXISTING data only — document_get / document_list /
// ledger_list read verbs — and never write a ledger row or patch a status
// (reviewer-only stays intact). Episodes are grouped by internal/taskepisode,
// the SAME projection `satellites task executions` uses.
package server

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/taskepisode"
	"github.com/bobmcallan/satellites/internal/verb"
)

var taskDetailTmpl = template.Must(template.ParseFS(assets,
	"templates/task_detail.html", "templates/_user_menu.html"))

// taskLedgerPageLimit bounds one ledger_list page; the detail pages the full
// ledger so every episode (across many runs) is grouped.
const taskLedgerPageLimit = 200

// taskRow is one task in the project-detail tasks panel — peer to storyRow.
type taskRow struct {
	ID       string
	Title    string
	Status   string
	Priority string
	Category string // rendered as its own chip, peer to storyRow.Category
	Tags     []string
	RunCount int    // execution episodes derived from the ledger
	LastRun  string // formatted start of the most recent episode ("" = never run)
}

// taskEpisodeView is one execution episode for the detail Executions view: the
// projected run metadata plus the ledger/log rows that fall inside it.
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
	Transition string // "from → to" for a status_transition ("" otherwise)
}

type taskDetailData struct {
	Title       string
	TaskID      string
	TaskTitle   string
	Status      string
	ProjectID   string
	Body        template.HTML
	Episodes    []taskEpisodeView
	UserEmail   string
	UserName    string
	UserAvatar  string
	ActiveNav   string
	FooterName  string
	FooterEmail string
	Version     string
}

// dispatchTaskList lists a project's tasks (document_list type:task) for the
// project-detail panel. Read-only.
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
		// Episode summary (run-count + last-run) from the ledger — best-effort;
		// a ledger read error leaves the counts zero rather than failing the page.
		if eps, lErr := taskEpisodes(ctx, d.ID); lErr == nil && len(eps) > 0 {
			row.RunCount = len(eps)
			row.LastRun = eps[len(eps)-1].Start.Format("2006-01-02 15:04")
		} else if lErr != nil {
			arbor.WarnCtx(ctx, "task_list: episodes", "id", d.ID, "err", lErr)
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

// taskEpisodes fetches the ledger and projects it into episodes (the shared
// projection). Used by both the list summary and the detail view.
func taskEpisodes(ctx context.Context, taskID string) ([]taskepisode.Episode, error) {
	entries, err := dispatchTaskLedger(ctx, taskID)
	if err != nil {
		return nil, err
	}
	rows := make([]taskepisode.Row, len(entries))
	for i, e := range entries {
		rows[i] = taskepisode.Row{Kind: e.kind, To: e.to, Created: e.created}
	}
	return taskepisode.Project(rows), nil
}

// buildTaskDetail resolves the read-only task detail view model: the task body
// plus its execution episodes, each carrying the ledger rows that fall inside
// it (sliced by the shared projection's StartIdx/Rows). Read-only.
func buildTaskDetail(ctx context.Context, taskID string) (taskDetailData, error) {
	getReq, _ := json.Marshal(verb.DocumentGetRequest{ID: taskID})
	raw, err := verb.Dispatch(ctx, "document_get", getReq)
	if err != nil {
		return taskDetailData{}, err
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
		return taskDetailData{}, err
	}

	data := taskDetailData{
		TaskID:    got.Document.ID,
		TaskTitle: got.Document.Name,
		Status:    got.Document.Status,
		ProjectID: got.Document.ProjectID,
	}
	if strings.TrimSpace(got.RawBody) != "" {
		data.Body = renderMarkdown(got.RawBody)
	}

	entries, lErr := dispatchTaskLedger(ctx, taskID)
	if lErr != nil {
		arbor.WarnCtx(ctx, "task_detail: ledger", "id", taskID, "err", lErr)
		return data, nil
	}
	rows := make([]taskepisode.Row, len(entries))
	for i, e := range entries {
		rows[i] = taskepisode.Row{Kind: e.kind, To: e.to, Created: e.created}
	}
	for _, ep := range taskepisode.Project(rows) {
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
		// The episode's entries are exactly entries[StartIdx : StartIdx+Rows]
		// (the rows slice is 1:1 with entries) — the shared projection's slice,
		// no second walk.
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
		data.Episodes = append(data.Episodes, ev)
	}
	return data, nil
}

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

		data, err := buildTaskDetail(ctx, taskID)
		if err != nil {
			arbor.WarnCtx(ctx, "task_detail: build", "id", taskID, "err", err)
			http.NotFound(w, r)
			return
		}
		data.Title = data.TaskTitle + " · tasks · satellites"
		data.ActiveNav = "projects"
		data.FooterName = footerName
		data.FooterEmail = footerEmail
		data.Version = versionString()
		if cfg.Store != nil && cfg.Store.DB != nil {
			if u, err := cfg.Store.GetUserByID(ctx, userID); err == nil && u != nil {
				data.UserEmail = u.Email
				data.UserName = u.DisplayName
				data.UserAvatar = avatarLetter(u.DisplayName, u.Email)
			}
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := taskDetailTmpl.Execute(w, data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}
