// Portal per-story QA process-visibility view (sty_0915e1b7).
//
// GET /stories/{id} renders the DECLARED workflow (states/transitions/gates,
// resolved from the story type via the dynamic skill index) overlaid with the
// ACTUAL process from the ledger — a read-only reconciliation. The view never
// writes a ledger row or patches a status; it only reports what the loop
// recorded.
//
// GET /api/stories/{id}/events is the realtime companion: a Server-Sent-Events
// stream that pushes newly-appended ledger rows for the story so the view
// updates live as gates fire, with no full-page poll from the browser.
//
// Transport layering (pr_mcp_cli_shared_path): this file reaches the substrate
// only through internal/verb's Dispatch. It does NOT import internal/ledger —
// the ledger_list response rows are mapped into processtrace.LedgerEntry (a
// transport-neutral projection) the same way ledger.go mirrors them for
// rendering. internal/workflow + internal/processtrace are pure domain helpers,
// not substrate-store packages, so they are not under the import ban.

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/processtrace"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workflow"
)

var storyDetailTmpl = template.Must(template.ParseFS(assets,
	"templates/story_detail.html", "templates/_user_menu.html"))

const (
	storyEventsPollInterval = 2 * time.Second
	storyEventsMaxLifetime  = 10 * time.Minute // browser EventSource auto-reconnects
	storyLedgerLimit        = 200
)

type storyTraceRowView struct {
	From        string
	To          string
	Gate        string
	Status      string // accepted | rejected | pending | fired | not_reached
	Badge       string // ✓ / ✗ / ● / ◦
	Verdict     string
	StepSummary string
	Actor       string
	When        string
	RejectCount int
}

type storyDetailData struct {
	Title         string
	StoryID       string
	StoryTitle    string
	StoryType     string
	CurrentStatus string
	WorkflowName  string
	NoWorkflow    bool
	Rows          []storyTraceRowView
	UserEmail     string
	UserName      string
	UserAvatar    string
	ActiveNav     string
	FooterName    string
	FooterEmail   string
	Version       string
}

func storyDetailHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := cfg.Sessions.UserID(r)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		ctx := withSessionUser(r.Context(), cfg, userID)

		storyID := strings.TrimSpace(path.Base(r.URL.Path))
		if storyID == "" || storyID == "stories" {
			http.NotFound(w, r)
			return
		}

		story, err := dispatchStoryMeta(ctx, storyID)
		if err != nil {
			arbor.WarnCtx(ctx, "story_detail: document_get", "id", storyID, "err", err)
			http.NotFound(w, r)
			return
		}

		data := storyDetailData{
			Title:         story.Title + " · stories · satellites",
			StoryID:       story.ID,
			StoryTitle:    story.Title,
			StoryType:     story.Category,
			CurrentStatus: story.Status,
			ActiveNav:     "projects",
			FooterName:    footerName,
			FooterEmail:   footerEmail,
			Version:       versionString(),
		}
		if cfg.Store != nil && cfg.Store.DB != nil {
			if u, err := cfg.Store.GetUserByID(ctx, userID); err == nil && u != nil {
				data.UserEmail = u.Email
				data.UserName = u.DisplayName
				data.UserAvatar = avatarLetter(u.DisplayName, u.Email)
			}
		}

		wf, err := resolveWorkflowForStory(ctx, story.Category)
		if err != nil || wf == nil {
			if err != nil {
				arbor.WarnCtx(ctx, "story_detail: resolve workflow", "type", story.Category, "err", err)
			}
			data.NoWorkflow = true
		} else {
			entries, lErr := dispatchStoryLedger(ctx, storyID, time.Time{})
			if lErr != nil {
				arbor.WarnCtx(ctx, "story_detail: ledger_list", "id", storyID, "err", lErr)
			}
			trace := processtrace.Reconcile(storyID, story.Category, story.Status, wf, entries)
			data.WorkflowName = trace.WorkflowName
			data.Rows = traceRows(trace)
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := storyDetailTmpl.Execute(w, data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// storyEventsHandler streams new ledger rows for a story as SSE. On connect it
// anchors at "now" and, on a short server-side tick, pushes any entry appended
// since the last seen timestamp. The browser client reloads the view on each
// event so the freshly-fired gate verdict appears live.
func storyEventsHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := cfg.Sessions.UserID(r)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := withSessionUser(r.Context(), cfg, userID)
		storyID := strings.TrimSpace(r.PathValue("id"))
		if storyID == "" {
			http.NotFound(w, r)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		// Anchor at the newest existing entry so we only stream what arrives
		// after the client connected.
		since := time.Now().UTC()
		if existing, err := dispatchStoryLedger(ctx, storyID, time.Time{}); err == nil {
			for _, e := range existing {
				if e.CreatedAt.After(since) {
					since = e.CreatedAt
				}
			}
		}
		fmt.Fprint(w, ": connected\n\n")
		flusher.Flush()

		ticker := time.NewTicker(storyEventsPollInterval)
		defer ticker.Stop()
		deadline := time.NewTimer(storyEventsMaxLifetime)
		defer deadline.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-deadline.C:
				return
			case <-ticker.C:
				fresh, since2 := ledgerEntriesSince(ctx, storyID, since)
				since = since2
				for _, e := range fresh {
					fmt.Fprintf(w, "event: ledger\ndata: %s\n\n", sseData(e))
				}
				if len(fresh) > 0 {
					flusher.Flush()
				}
			}
		}
	}
}

// ledgerEntriesSince returns the story's ledger rows newer than `since`,
// oldest-first, and the high-water timestamp to anchor the next poll. Testable
// seam for the realtime path. A dispatch error yields no entries and the same
// anchor (the stream simply waits for the next tick).
func ledgerEntriesSince(ctx context.Context, storyID string, since time.Time) ([]processtrace.LedgerEntry, time.Time) {
	entries, err := dispatchStoryLedger(ctx, storyID, since)
	if err != nil {
		return nil, since
	}
	out := make([]processtrace.LedgerEntry, 0, len(entries))
	high := since
	for _, e := range entries {
		if e.CreatedAt.After(since) {
			out = append(out, e)
			if e.CreatedAt.After(high) {
				high = e.CreatedAt
			}
		}
	}
	return out, high
}

// sseData renders a single SSE data line (one line, newline-free) describing a
// ledger row. The client only needs a signal that something changed; the kind
// is included for debuggability.
func sseData(e processtrace.LedgerEntry) string {
	b, _ := json.Marshal(map[string]string{"kind": e.Kind, "at": e.CreatedAt.UTC().Format(time.RFC3339)})
	return string(b)
}

// storyMeta is the minimal story projection the view needs.
type storyMeta struct {
	ID       string
	Title    string
	Status   string
	Category string
}

func dispatchStoryMeta(ctx context.Context, id string) (storyMeta, error) {
	body, _ := json.Marshal(verb.DocumentGetRequest{ID: id})
	raw, err := verb.Dispatch(ctx, "document_get", body)
	if err != nil {
		return storyMeta{}, err
	}
	var resp verb.DocumentGetResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return storyMeta{}, err
	}
	if resp.Document.Type != "story" {
		return storyMeta{}, fmt.Errorf("document %s is not a story", id)
	}
	return storyMeta{
		ID:       resp.Document.ID,
		Title:    resp.Document.Name,
		Status:   resp.Document.Status,
		Category: resp.Document.Category,
	}, nil
}

// resolveWorkflowForStory returns the workflow whose applies_to contains the
// story type, resolved through the dynamic skill index (effective, so a user's
// overridden workflow wins for that viewer — sty_cbeeb452). Returns nil (no
// error) when no workflow governs the type.
//
// Observability only (see principle process-as-configuration, "the boundary"):
// this parses a workflow purely to RENDER the story-detail view. It decides
// and advances nothing — gating authority lives in the gate skill, never here.
func resolveWorkflowForStory(ctx context.Context, storyType string) (*workflow.Workflow, error) {
	storyType = strings.TrimSpace(storyType)
	if storyType == "" {
		return nil, nil
	}
	listBody, _ := json.Marshal(verb.DocumentListRequest{Type: "skill", Effective: true, Limit: storyLedgerLimit})
	raw, err := verb.Dispatch(ctx, "document_list", listBody)
	if err != nil {
		return nil, err
	}
	var resp verb.DocumentListResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	for _, d := range resp.Items {
		getBody, _ := json.Marshal(verb.DocumentGetRequest{ID: d.ID})
		graw, gErr := verb.Dispatch(ctx, "document_get", getBody)
		if gErr != nil {
			continue
		}
		var gr verb.DocumentGetResponse
		if json.Unmarshal(graw, &gr) != nil {
			continue
		}
		wf, pErr := workflow.Parse([]byte(gr.RawBody))
		if pErr != nil || wf == nil {
			continue // not a workflow skill (gate/capability skills have no states block)
		}
		for _, at := range wf.AppliesTo {
			if strings.EqualFold(strings.TrimSpace(at), storyType) {
				return wf, nil
			}
		}
	}
	return nil, nil
}

// dispatchStoryLedger reads a story's ledger via the ledger_list verb and maps
// each row into processtrace.LedgerEntry. Ranging the verb response rows does
// not name the internal/ledger type, keeping this transport import-clean.
func dispatchStoryLedger(ctx context.Context, storyID string, after time.Time) ([]processtrace.LedgerEntry, error) {
	req := verb.LedgerListRequest{StoryID: storyID, Limit: storyLedgerLimit}
	if !after.IsZero() {
		req.CreatedAfter = after.UTC().Format(time.RFC3339)
	}
	body, _ := json.Marshal(req)
	raw, err := verb.Dispatch(ctx, "ledger_list", body)
	if err != nil {
		return nil, err
	}
	var resp verb.LedgerListResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	out := make([]processtrace.LedgerEntry, 0, len(resp.Entries))
	for _, e := range resp.Entries {
		out = append(out, processtrace.LedgerEntry{
			Kind:      e.Kind,
			Body:      e.Body,
			Payload:   e.Payload,
			Actor:     e.Actor,
			CreatedAt: e.CreatedAt,
		})
	}
	return out, nil
}

func traceRows(trace processtrace.ProcessTrace) []storyTraceRowView {
	rows := make([]storyTraceRowView, 0, len(trace.Transitions))
	for _, t := range trace.Transitions {
		row := storyTraceRowView{
			From:        t.From,
			To:          t.To,
			Gate:        t.ReviewerSkill,
			Status:      t.Status,
			Badge:       statusBadge(t.Status),
			Verdict:     t.Verdict,
			StepSummary: t.StepSummary,
			Actor:       t.Actor,
			RejectCount: t.RejectCount,
		}
		if row.Gate == "" {
			row.Gate = "—"
		}
		if t.At != nil {
			row.When = t.At.UTC().Format("2006-01-02 15:04")
		} else {
			row.When = "—"
		}
		rows = append(rows, row)
	}
	return rows
}

func statusBadge(status string) string {
	switch status {
	case processtrace.StatusAccepted, processtrace.StatusFired:
		return "✓"
	case processtrace.StatusRejected:
		return "✗"
	case processtrace.StatusPending:
		return "●"
	default:
		return "◦"
	}
}
