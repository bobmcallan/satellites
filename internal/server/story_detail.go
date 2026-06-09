// Portal per-story QA process-visibility view (sty_0915e1b7).
//
// GET /stories/{id} renders the DECLARED workflow (states/transitions/gates,
// resolved from the story type via the dynamic skill index) overlaid with the
// ACTUAL process from the ledger — a read-only reconciliation. The view never
// writes a ledger row or patches a status; it only reports what the loop
// recorded.
//
// GET /stories/{id}/trace.fragment is the realtime companion (sty_96cc0ade):
// the shared SSE bus client (live.js) refetches it on a story-scoped trigger
// and swaps the trace region in place, so the view updates live as gates fire
// with no full-page reload. It replaces the retired per-story SSE endpoint
// (/api/stories/{id}/events, sty_0915e1b7).
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

const storyLedgerLimit = 200

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
	Title              string
	StoryID            string
	StoryTitle         string
	StoryType          string
	CurrentStatus      string
	WorkflowName       string
	NoWorkflow         bool
	Description        template.HTML
	AcceptanceCriteria template.HTML
	Rows               []storyTraceRowView
	UserEmail          string
	UserName           string
	UserAvatar         string
	ActiveNav          string
	FooterName         string
	FooterEmail        string
	Version            string
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

		data, err := buildStoryDetail(ctx, storyID)
		if err != nil {
			arbor.WarnCtx(ctx, "story_detail: build", "id", storyID, "err", err)
			http.NotFound(w, r)
			return
		}
		data.Title = data.StoryTitle + " · stories · satellites"
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
		if err := storyDetailTmpl.Execute(w, data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// buildStoryDetail resolves the read-only story-trace view model (story meta +
// declared workflow reconciled against the ledger) for storyID. Shared by the
// full-page handler and the live trace fragment (sty_96cc0ade). Read-only: it
// dispatches only read verbs and never writes a ledger row or patches status.
func buildStoryDetail(ctx context.Context, storyID string) (storyDetailData, error) {
	story, err := dispatchStoryMeta(ctx, storyID)
	if err != nil {
		return storyDetailData{}, err
	}
	data := storyDetailData{
		StoryID:       story.ID,
		StoryTitle:    story.Title,
		StoryType:     story.Category,
		CurrentStatus: story.Status,
	}
	// Render the story body + ACs as markdown for the readable expanded view
	// (sty_c04acfc7), via the same safe goldmark renderer the changelog uses.
	if strings.TrimSpace(story.Description) != "" {
		data.Description = renderMarkdown(story.Description)
	}
	if strings.TrimSpace(story.AcceptanceCriteria) != "" {
		data.AcceptanceCriteria = renderMarkdown(story.AcceptanceCriteria)
	}
	wf, err := resolveWorkflowForStory(ctx, story)
	if err != nil || wf == nil {
		if err != nil {
			arbor.WarnCtx(ctx, "story_detail: resolve workflow", "type", story.Category, "err", err)
		}
		data.NoWorkflow = true
		return data, nil
	}
	entries, lErr := dispatchStoryLedger(ctx, storyID, time.Time{})
	if lErr != nil {
		arbor.WarnCtx(ctx, "story_detail: ledger_list", "id", storyID, "err", lErr)
	}
	trace := processtrace.Reconcile(storyID, story.Category, story.Status, wf, entries)
	data.WorkflowName = trace.WorkflowName
	data.Rows = traceRows(trace)
	return data, nil
}

// storyTraceFragmentHandler renders just the live trace region (status pill +
// process-trace table) for a story — the refetch target the shared SSE client
// swaps in on a story-scoped trigger (sty_96cc0ade), replacing the retired
// per-story SSE endpoint. Read-only; same read verbs; no new MCP verb.
func storyTraceFragmentHandler(cfg Config) http.HandlerFunc {
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
		data, err := buildStoryDetail(ctx, storyID)
		if err != nil {
			arbor.WarnCtx(ctx, "story_trace_fragment: build", "id", storyID, "err", err)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := storyDetailTmpl.ExecuteTemplate(w, "story-trace", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// storyMeta is the minimal story projection the view needs. ProjectID +
// WorkspaceID bound the workflow resolver to the story's OWN project so a
// kind:workflow skill from another project can never be selected (sty_68379c96).
// Description (the story body) + AcceptanceCriteria are the readable content the
// expandable view renders as markdown (sty_c04acfc7).
type storyMeta struct {
	ID                 string
	Title              string
	Status             string
	Category           string
	ProjectID          string
	WorkspaceID        string
	Description        string
	AcceptanceCriteria string
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
		ID:                 resp.Document.ID,
		Title:              resp.Document.Name,
		Status:             resp.Document.Status,
		Category:           resp.Document.Category,
		ProjectID:          resp.Document.ProjectID,
		WorkspaceID:        resp.Document.WorkspaceID,
		Description:        resp.RawBody,
		AcceptanceCriteria: resp.Document.AcceptanceCriteria,
	}, nil
}

// resolveWorkflowForStory returns the workflow whose applies_to contains the
// story type, resolved through the dynamic skill index (effective, so a user's
// overridden workflow wins for that viewer — sty_cbeeb452). Returns nil (no
// error) when no workflow governs the type.
//
// Selection is bounded to the story's OWN project (sty_68379c96; cf.
// cross-project skill leak sty_2fa6f087): a candidate skill is eligible only
// when it is visible to the story's project — a system-scoped workflow (applies
// everywhere), the viewer's own user-scope override, the story's workspace, or
// the story's project. A project- or workspace-scoped kind:workflow skill owned
// by a DIFFERENT project/workspace can never be selected, even when its
// applies_to overlaps and it sorts first by created_at. The bound is applied to
// the RESULT rows, not the query: a Scope:"all" query would newly require the
// viewer to be a workspace member (authorizeListScope), which is stricter than
// the page's existing visibility (the id-addressed document_get this view rides
// on performs no membership check) and would silently blank the trace for a
// non-member who can otherwise open the story.
//
// Observability only (see principle process-as-configuration, "the boundary"):
// this parses a workflow purely to RENDER the story-detail view. It decides
// and advances nothing — gating authority lives in the gate skill, never here.
func resolveWorkflowForStory(ctx context.Context, story storyMeta) (*workflow.Workflow, error) {
	storyType := strings.TrimSpace(story.Category)
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
		// Project-isolation bound: skip a workflow that belongs to another
		// project/workspace so the view never borrows it (sty_68379c96).
		switch string(d.Scope) {
		case "system", "user":
			// system applies everywhere; a user row is the caller's own override.
		case "workspace":
			if story.WorkspaceID == "" || d.WorkspaceID != story.WorkspaceID {
				continue
			}
		case "project":
			if story.ProjectID == "" || d.ProjectID != story.ProjectID {
				continue
			}
		default:
			continue
		}
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
