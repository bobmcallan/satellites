// `satellites evidence audit` — the background QA audit (sty_8ba432e7). It reads
// a story's (or the project's) durable ledger trail via ledger_list — the
// captured signal order:8 made durable — maps it into the processtrace audit's
// LedgerEntry projection, and surfaces the detected anomalies to the operator.
// Read-only and out of band: it never writes a ledger row, patches a status, or
// enters the executor's turn (the epic anti-goal).
//
// The anomaly detection lives in internal/processtrace (Audit), beside the
// declared-vs-actual Reconcile it composes with; this file is the transport that
// feeds it the captured signal and renders findings.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/bobmcallan/satellites/internal/processtrace"
	"github.com/bobmcallan/satellites/internal/verb"
)

type evidenceAuditOpts struct {
	Story     string // one story; empty ⇒ sweep the project
	ProjectID string
	JSON      bool
}

// runEvidenceAudit assembles each target story's captured ledger trail, runs the
// processtrace audit, and prints the findings. dispatch is injected so the core
// is testable without a server.
func runEvidenceAudit(ctx context.Context, out io.Writer, opts evidenceAuditOpts, dispatch verbDispatch) error {
	var targets []string
	if opts.Story != "" {
		targets = []string{opts.Story}
	} else {
		ids, err := auditStoryIDs(ctx, dispatch, opts.ProjectID)
		if err != nil {
			return fmt.Errorf("evidence audit: list stories: %w", err)
		}
		targets = ids
	}

	var findings []processtrace.Finding
	for _, id := range targets {
		sa, err := auditAssembleStory(ctx, dispatch, id)
		if err != nil {
			fmt.Fprintf(out, "warn: skip %s: %v\n", id, err)
			continue
		}
		findings = append(findings, processtrace.Audit(sa)...)
	}

	if opts.JSON {
		if findings == nil {
			findings = []processtrace.Finding{}
		}
		return json.NewEncoder(out).Encode(findings)
	}
	if len(findings) == 0 {
		fmt.Fprintf(out, "audit: %d story(ies) clean — no anomalies\n", len(targets))
		return nil
	}
	for _, f := range findings {
		fmt.Fprintf(out, "%-7s %-28s %s\n  %s\n", f.Severity, f.Anomaly, f.StoryID, f.Detail)
	}
	fmt.Fprintf(out, "\naudit: %d finding(s) across %d story(ies)\n", len(findings), len(targets))
	return nil
}

// auditStoryIDs lists the project's story ids for the project-wide sweep.
func auditStoryIDs(ctx context.Context, dispatch verbDispatch, projectID string) ([]string, error) {
	req, err := json.Marshal(verb.DocumentListRequest{Type: "story", ProjectID: projectID, Limit: 500})
	if err != nil {
		return nil, err
	}
	raw, err := dispatch(ctx, "document_list", req)
	if err != nil {
		return nil, err
	}
	var listed struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(listed.Items))
	for _, it := range listed.Items {
		if it.ID != "" {
			ids = append(ids, it.ID)
		}
	}
	return ids, nil
}

// auditAssembleStory fetches one story's status/type + its full ledger trail and
// projects them into the processtrace audit's input shape. Minimal local
// projections keep the CLI clear of the substrate-domain import ban.
func auditAssembleStory(ctx context.Context, dispatch verbDispatch, storyID string) (processtrace.StoryAudit, error) {
	getReq, err := json.Marshal(verb.DocumentGetRequest{ID: storyID})
	if err != nil {
		return processtrace.StoryAudit{}, err
	}
	getRaw, err := dispatch(ctx, "document_get", getReq)
	if err != nil {
		return processtrace.StoryAudit{}, fmt.Errorf("get story: %w", err)
	}
	var got struct {
		Document struct {
			ID     string `json:"id"`
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"document"`
	}
	if err := json.Unmarshal(getRaw, &got); err != nil {
		return processtrace.StoryAudit{}, fmt.Errorf("decode story: %w", err)
	}
	if got.Document.Type != "story" {
		return processtrace.StoryAudit{}, fmt.Errorf("document %s is type=%q, not a story", storyID, got.Document.Type)
	}

	entries, err := auditLedger(ctx, dispatch, storyID)
	if err != nil {
		return processtrace.StoryAudit{}, err
	}
	return processtrace.StoryAudit{
		StoryID:       got.Document.ID,
		StoryType:     got.Document.Type,
		CurrentStatus: got.Document.Status,
		Entries:       entries,
	}, nil
}

// auditLedger reads a story's full ledger trail and maps it into the audit's
// LedgerEntry projection.
func auditLedger(ctx context.Context, dispatch verbDispatch, storyID string) ([]processtrace.LedgerEntry, error) {
	req, err := json.Marshal(verb.LedgerListRequest{StoryID: storyID, Limit: 1000})
	if err != nil {
		return nil, err
	}
	raw, err := dispatch(ctx, "ledger_list", req)
	if err != nil {
		return nil, fmt.Errorf("ledger_list: %w", err)
	}
	var ll struct {
		Entries []struct {
			Kind      string          `json:"kind"`
			Body      string          `json:"body"`
			Payload   json.RawMessage `json:"payload"`
			Actor     string          `json:"actor"`
			CreatedAt time.Time       `json:"created_at"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &ll); err != nil {
		return nil, fmt.Errorf("decode ledger: %w", err)
	}
	out := make([]processtrace.LedgerEntry, 0, len(ll.Entries))
	for _, e := range ll.Entries {
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
