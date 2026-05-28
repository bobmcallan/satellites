// Ledger verbs — operator-facing append + list against the evidence
// ledger (internal/ledger). Append-only is enforced at the DB layer;
// this file is the JSON wire shape + registry wiring + role gate.
//
// As of sty_0006f5f5 the ledger carries operator-observability rows
// beyond the original story-event log: every row may be tagged with
// any subset of {story, project, workspace, session, run} ids so the
// portal /ledger page (sty_becb7019) can isolate one `claude -p` run
// from another. The verb input shape is additive — legacy callers
// still pass story_id alone and round-trip identically.

package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/ledger"
)

var ledgerStore *ledger.Store

// SetLedgerStore wires the server's ledger.Store into the verb
// package. Called from cmd/satellites-server on boot.
func SetLedgerStore(s *ledger.Store) { ledgerStore = s }

// LedgerAppendRequest is the wire shape for ledger_append. Every
// correlation id is optional; only kind is required. story_id is
// retained as the first field for legacy callers that still write
// story-only rows.
type LedgerAppendRequest struct {
	StoryID     string          `json:"story_id,omitempty"`
	ProjectID   string          `json:"project_id,omitempty"`
	WorkspaceID string          `json:"workspace_id,omitempty"`
	SessionID   string          `json:"session_id,omitempty"`
	RunID       string          `json:"run_id,omitempty"`
	Kind        string          `json:"kind"`
	Actor       string          `json:"actor,omitempty"`
	Body        string          `json:"body,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	Refs        json.RawMessage `json:"refs,omitempty"`
}

// LedgerListRequest filters by any subset of correlation ids, kind,
// body substring, and a created_at window. At least one of these
// must be set so the verb never executes an unfiltered scan over the
// table. Limit caps the page (server caps at 2000); Cursor paginates
// oldest-to-newest. CreatedAfter / CreatedBefore are RFC3339
// timestamps; the portal /ledger page defaults to a 24-hour window.
type LedgerListRequest struct {
	StoryID         string   `json:"story_id,omitempty"`
	ProjectID       string   `json:"project_id,omitempty"`
	WorkspaceID     string   `json:"workspace_id,omitempty"`
	SessionID       string   `json:"session_id,omitempty"`
	RunID           string   `json:"run_id,omitempty"`
	Kind            string   `json:"kind,omitempty"`
	BodyContainsAny []string `json:"body_contains_any,omitempty"` // OR'd ILIKE terms on body
	CreatedAfter    string   `json:"created_after,omitempty"`     // RFC3339
	CreatedBefore   string   `json:"created_before,omitempty"`    // RFC3339
	Limit           int      `json:"limit,omitempty"`
	Cursor          string   `json:"cursor,omitempty"`
}

// LedgerListResponse carries the matched rows plus the next cursor
// (empty when the page reached the tail of the matching set).
type LedgerListResponse struct {
	Entries    []ledger.Entry `json:"entries"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

func init() {
	Register(&Verb{
		Name:        "ledger_append",
		Description: "Append an entry to the evidence ledger. Append-only: entries cannot be updated or deleted once written. Correlation ids (story/project/workspace/session/run) are all optional; kind is required. Runner-role keys may append rows whose kind starts with 'log:'; reviewer keys may append any kind.",
		Invoke:      invokeLedgerAppend,
	})
	Register(&Verb{
		Name:        "ledger_list",
		Description: "List ledger entries, oldest-first. Pass any subset of story_id / project_id / workspace_id / session_id / run_id / kind as filters (at least one is required). Paginated via cursor.",
		Invoke:      invokeLedgerList,
	})
}

func invokeLedgerAppend(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if ledgerStore == nil {
		return nil, fmt.Errorf("ledger_append: store not configured")
	}
	var req LedgerAppendRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("ledger_append: bad request: %w", err)
		}
	}
	if strings.TrimSpace(req.Kind) == "" {
		return nil, fmt.Errorf("ledger_append: kind required")
	}
	if err := requireLedgerAppendRole(ctx, req.Kind); err != nil {
		return nil, err
	}
	actor := req.Actor
	if actor == "" {
		actor = actorFromContext(ctx)
	}
	e, err := ledgerStore.Append(ctx, ledger.AppendInput{
		StoryID:     req.StoryID,
		ProjectID:   req.ProjectID,
		WorkspaceID: req.WorkspaceID,
		SessionID:   req.SessionID,
		RunID:       req.RunID,
		Kind:        req.Kind,
		Actor:       actor,
		Body:        req.Body,
		Payload:     req.Payload,
		Refs:        req.Refs,
	}, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.StoryID) != "" {
		dispatchSummaryRegen(ctx, req.StoryID)
	}
	return json.Marshal(e)
}

func invokeLedgerList(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if ledgerStore == nil {
		return nil, fmt.Errorf("ledger_list: store not configured")
	}
	var req LedgerListRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("ledger_list: bad request: %w", err)
		}
	}
	f := ledger.ListFilter{
		StoryID:         req.StoryID,
		ProjectID:       req.ProjectID,
		WorkspaceID:     req.WorkspaceID,
		SessionID:       req.SessionID,
		RunID:           req.RunID,
		Kind:            req.Kind,
		BodyContainsAny: req.BodyContainsAny,
		Limit:           req.Limit,
		Cursor:          req.Cursor,
	}
	if s := strings.TrimSpace(req.CreatedAfter); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return nil, fmt.Errorf("ledger_list: created_after: %w", err)
		}
		f.CreatedAfter = t
	}
	if s := strings.TrimSpace(req.CreatedBefore); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return nil, fmt.Errorf("ledger_list: created_before: %w", err)
		}
		f.CreatedBefore = t
	}
	res, err := ledgerStore.List(ctx, f)
	if err != nil {
		return nil, err
	}
	return json.Marshal(LedgerListResponse{Entries: res.Entries, NextCursor: res.NextCursor})
}

// requireLedgerAppendRole gates ledger_append. The reviewer role keeps
// its full append rights (any kind); the runner role may only append
// log-kind rows (the arbor LedgerHandler path emits log:info, log:warn,
// etc.); other roles (executor) are refused. Unauthenticated callers
// (CLI in-process, JWT portal users with no api-key context) pass —
// they're gated by a separate membership check upstream.
func requireLedgerAppendRole(ctx context.Context, kind string) error {
	role := auth.APIKeyRoleFromContext(ctx)
	switch role {
	case "", auth.APIKeyRoleReviewer:
		return nil
	case auth.APIKeyRoleRunner:
		if strings.HasPrefix(kind, ledger.LogKindPrefix) {
			return nil
		}
		return fmt.Errorf("ledger_append: %w: runner-role api-key may only append log-kind rows (kind=%s)", ErrForbidden, kind)
	}
	return fmt.Errorf("ledger_append: %w: requires reviewer-role or runner-role api-key (got %s)", ErrForbidden, role)
}

// actorFromContext returns the authenticated user id when present
// (HTTP/MCP transports), otherwise empty (CLI-local in-process).
// Empty actor is the canonical "system / unattributed" marker on
// ledger rows.
func actorFromContext(ctx context.Context) string {
	if u := auth.FromContext(ctx); u != nil {
		return u.ID
	}
	return ""
}
