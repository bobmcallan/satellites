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
	"net/url"
	"path"
	"sort"
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

// storyMergedRowView is one row of the merged Ledger/Log view
// (epic:graduated-workflow ledger-log-merge): the append-only ledger entry
// plus the workflow-edge badge AnnotateLedger resolved for it. The ledger is
// the spine; the workflow only annotates.
type storyMergedRowView struct {
	When       string
	Kind       string
	Actor      string
	Body       string
	Transition string // "from → to" ("" = no edge)
	BadgeClass string // exhausted | gate-clean | gate-blocked | ci | ""
	BadgeLabel string // rendered badge text ("" = none beyond Transition)
}

// storyCloseOutView is the estimate-vs-actual close-out block. Every field is
// derived from the embedded workflow + existing ledger rows; TokensActual
// renders "—" until a metrics source records token actuals.
type storyCloseOutView struct {
	Show           bool
	Terminal       bool
	EstimateTime   string
	EstimateTokens string
	EstimateBasis  string
	ActualElapsed  string
	ActualTokens   string
	TotalRejects   int
}

type storyDetailData struct {
	Title              string
	StoryID            string
	StoryTitle         string
	StoryType          string
	CurrentStatus      string
	WorkflowName       string
	WorkflowSource     string // "embedded" (story's own ## Workflow) or "type" (applies_to fallback)
	NoWorkflow         bool
	CloseOut           storyCloseOutView
	Description        template.HTML
	AcceptanceCriteria template.HTML
	MergedRows         []storyMergedRowView
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

		// The standalone story page is deprecated (sty_0633bcf5): the
		// project-detail inline panel is the single canonical story view.
		// Redirect to it with the story deep-linked open. The trace/ledger
		// fragment routes are registered more specifically and still served
		// directly, so this only affects the full-page view.
		story, err := dispatchStoryMeta(ctx, storyID)
		if err != nil || strings.TrimSpace(story.ProjectID) == "" {
			if err != nil {
				arbor.WarnCtx(ctx, "story_detail: redirect resolve", "id", storyID, "err", err)
			}
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r,
			"/projects/"+url.PathEscape(story.ProjectID)+"?story="+url.QueryEscape(storyID),
			http.StatusFound)
	}
}

// buildStoryDetail resolves the read-only story-trace view model (story meta +
// declared workflow reconciled against the ledger) for storyID. Shared by the
// full-page handler and the live trace fragment (sty_96cc0ade). Read-only: it
// dispatches only read verbs and never writes a ledger row or patches status.
func buildStoryDetail(ctx context.Context, storyID string, resolveActorFn func(string) string) (storyDetailData, error) {
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
	// Fetch the ledger once, up front: it is the spine of the merged
	// Ledger/Log view and feeds the close-out derivation below.
	entries, lErr := dispatchStoryLedger(ctx, storyID, time.Time{})
	if lErr != nil {
		arbor.WarnCtx(ctx, "story_detail: ledger_list", "id", storyID, "err", lErr)
	}
	// The story's OWN embedded ## Workflow is the declared process — it wins
	// outright (sty_a4257811; the old type-first resolution reported "no
	// workflow governs" for stories that carried a complete embedded block).
	// Type-resolution (applies_to, incl. the "*" wildcard) survives only as
	// the fallback for a story with no embedded block, and the view says
	// which source it rendered from.
	wf, wfErr := workflow.ParseBody([]byte(story.Description))
	if wfErr == nil && wf != nil {
		data.WorkflowSource = "embedded"
	} else {
		wf, wfErr = resolveWorkflowForStory(ctx, story)
		if wfErr != nil {
			arbor.WarnCtx(ctx, "story_detail: resolve workflow", "type", story.Category, "err", wfErr)
		}
		if wf != nil {
			data.WorkflowSource = "type"
		}
	}
	// The merged rows render with or without a declared workflow — the
	// ledger is the spine; the workflow only annotates (nil wf = rows pass
	// through with gate/CI badges only).
	data.MergedRows = mergedRows(processtrace.AnnotateLedger(wf, entries), resolveActorFn)
	if wf == nil {
		data.NoWorkflow = true
		return data, nil
	}
	trace := processtrace.Reconcile(storyID, story.Category, story.Status, wf, entries, story.Tags)
	data.WorkflowName = trace.WorkflowName
	data.CloseOut = closeOutView(trace.CloseOut)
	return data, nil
}

// gatherStoryDocs lists the documents attached to a story (story outputs carry a
// `story:<id>` back-reference tag — see `satellites story output`) and renders
// each body as markdown for inline expand/collapse, newest-first. Read-only and
// best-effort: a list/body error degrades to fewer rows, never a page error
// (sty_bf2fc8e1). Mirrors the project documents panel (gatherDocPanelFrom).
func gatherStoryDocs(ctx context.Context, storyID string, q url.Values) ([]docRow, int, int) {
	// Reuse the project documents panel's gather (shared filter grammar, counts,
	// tag chips, body render) scoped to this story's documents — the story
	// Documents tab gets the same search/filter/count as the project panel
	// (sty_c017a274). Story outputs carry a `story:<id>` back-reference tag.
	listReq := verb.DocumentListRequest{Type: "document", Tags: []string{"story:" + storyID}, Limit: 200}
	rows, filtered, total, err := gatherDocPanelFrom(ctx, listReq, q)
	if err != nil {
		arbor.WarnCtx(ctx, "story_detail: list documents", "id", storyID, "err", err)
		return nil, 0, 0
	}
	// Newest-first, matching the prior story-tab ordering.
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].UpdatedAt.After(rows[j].UpdatedAt) })
	return rows, filtered, total
}

// mergedRows maps annotated ledger entries into the merged Ledger/Log view,
// newest first so the latest activity reads at the top. Badge classes are
// derived from the annotation, never from state or gate names.
func mergedRows(entries []processtrace.AnnotatedEntry, resolve func(string) string) []storyMergedRowView {
	rows := make([]storyMergedRowView, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		when := "—"
		if !e.CreatedAt.IsZero() {
			when = e.CreatedAt.UTC().Format("2006-01-02 15:04")
		}
		// Resolve the actor id to its email (sty_4a3a9ebf) — the operator-facing
		// identity — falling back to the raw id when unresolved.
		actor := strings.TrimSpace(resolveActor(resolve, e.Actor))
		if actor == "" {
			actor = "—"
		}
		row := storyMergedRowView{
			When:       when,
			Kind:       e.Kind,
			Actor:      actor,
			Body:       e.Body,
			Transition: e.Transition,
		}
		switch {
		case e.Exhausted:
			row.BadgeClass = "exhausted"
			row.BadgeLabel = "exhausted"
		case e.Gate != "":
			row.BadgeLabel = fmt.Sprintf("%s: %s", e.Gate, e.GateVerdict)
			row.BadgeClass = "gate-clean"
			if !strings.EqualFold(e.GateVerdict, "clean") {
				row.BadgeClass = "gate-blocked"
			}
		case e.Stage != "":
			row.BadgeLabel = fmt.Sprintf("ci %s: %s", e.Stage, e.GateVerdict)
			row.BadgeClass = "ci"
		}
		rows = append(rows, row)
	}
	return rows
}

// closeOutView renders the close-out derivation for the Result view. "—"
// marks a slot with no recorded value (notably token actuals, which await a
// reviewer-metrics source).
func closeOutView(co processtrace.CloseOut) storyCloseOutView {
	v := storyCloseOutView{
		Show:           true,
		Terminal:       co.Terminal,
		EstimateTime:   "—",
		EstimateTokens: "—",
		ActualElapsed:  "—",
		ActualTokens:   "—",
		TotalRejects:   co.TotalRejects,
	}
	if co.Estimate != nil {
		if co.Estimate.TimeMinutes > 0 {
			v.EstimateTime = fmt.Sprintf("%dm", co.Estimate.TimeMinutes)
		}
		if co.Estimate.Tokens > 0 {
			v.EstimateTokens = fmt.Sprintf("%dk", co.Estimate.Tokens/1000)
		}
		v.EstimateBasis = co.Estimate.Basis
	}
	if co.ElapsedMinutes > 0 {
		v.ActualElapsed = fmt.Sprintf("%dm", co.ElapsedMinutes)
	}
	if co.TokensActual != nil {
		v.ActualTokens = fmt.Sprintf("%dk", *co.TokensActual/1000)
	}
	return v
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
		data, err := buildStoryDetail(ctx, storyID, newActorResolver(ctx, cfg))
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

// storyDocsData is the view model for the story Documents fragment. Query is the
// active search (rehydrated into the box); Filtered/Total feed the count badge
// (sty_c017a274).
type storyDocsData struct {
	Documents []docRow
	Query     string
	Filtered  int
	Total     int
}

// storyDocsFragmentHandler renders just the attached-documents list for a story
// — the lazy-load target the inline panel's Documents tab fetches on first open
// (sty_bf2fc8e1), peer to the trace fragment. Plain HTML (native <details> for
// expand/collapse) so the inline-panel innerHTML swap needs no Alpine. Read-only.
func storyDocsFragmentHandler(cfg Config) http.HandlerFunc {
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
		q := r.URL.Query()
		rows, filtered, total := gatherStoryDocs(ctx, storyID, q)
		data := storyDocsData{Documents: rows, Query: strings.TrimSpace(q.Get("docs_q")), Filtered: filtered, Total: total}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := storyDetailTmpl.ExecuteTemplate(w, "story-docs", data); err != nil {
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
	Tags               []string
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
		Tags:               resp.Document.Tags,
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
	var cands []*workflow.Workflow
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
		cands = append(cands, wf)
	}
	return pickWorkflow(cands, storyType), nil
}

// pickWorkflow selects the workflow governing storyType from the candidates:
// an exact applies_to category match wins; a workflow declaring "*" governs
// any category as the fallback (sty_20d71a66 single-workflow) — so a repo
// need not enumerate its categories to be fully governed. Nil when nothing
// applies.
func pickWorkflow(cands []*workflow.Workflow, storyType string) *workflow.Workflow {
	var wildcard *workflow.Workflow
	for _, wf := range cands {
		for _, at := range wf.AppliesTo {
			switch strings.TrimSpace(at) {
			case "*":
				if wildcard == nil {
					wildcard = wf
				}
			default:
				if strings.EqualFold(strings.TrimSpace(at), storyType) {
					return wf
				}
			}
		}
	}
	return wildcard
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
