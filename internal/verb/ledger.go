// Ledger verbs — operator-facing append + list against the story
// event log (internal/ledger). Append-only is enforced at the DB
// layer; this file is the JSON wire shape + registry wiring.

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

type LedgerAppendRequest struct {
	StoryID string          `json:"story_id"`
	Kind    string          `json:"kind"`
	Actor   string          `json:"actor,omitempty"`
	Body    string          `json:"body,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Refs    json.RawMessage `json:"refs,omitempty"`
}

type LedgerListRequest struct {
	StoryID string `json:"story_id"`
	Kind    string `json:"kind,omitempty"`
}

type LedgerListResponse struct {
	Entries []ledger.Entry `json:"entries"`
}

func init() {
	Register(&Verb{
		Name:        "ledger_append",
		Description: "Append an entry to a story's ledger. Append-only: entries cannot be updated or deleted once written.",
		Invoke:      invokeLedgerAppend,
	})
	Register(&Verb{
		Name:        "ledger_list",
		Description: "List ledger entries for a story, oldest-first. Optional kind filter.",
		Invoke:      invokeLedgerList,
	})
}

func invokeLedgerAppend(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if ledgerStore == nil {
		return nil, fmt.Errorf("ledger_append: store not configured")
	}
	if err := requireReviewerRole(ctx, "ledger_append"); err != nil {
		return nil, err
	}
	var req LedgerAppendRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("ledger_append: bad request: %w", err)
		}
	}
	if strings.TrimSpace(req.StoryID) == "" {
		return nil, fmt.Errorf("ledger_append: story_id required")
	}
	if strings.TrimSpace(req.Kind) == "" {
		return nil, fmt.Errorf("ledger_append: kind required")
	}
	actor := req.Actor
	if actor == "" {
		actor = actorFromContext(ctx)
	}
	e, err := ledgerStore.Append(ctx, ledger.AppendInput{
		StoryID: req.StoryID,
		Kind:    req.Kind,
		Actor:   actor,
		Body:    req.Body,
		Payload: req.Payload,
		Refs:    req.Refs,
	}, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	dispatchSummaryRegen(ctx, req.StoryID)
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
	if strings.TrimSpace(req.StoryID) == "" {
		return nil, fmt.Errorf("ledger_list: story_id required")
	}
	entries, err := ledgerStore.List(ctx, req.StoryID, req.Kind)
	if err != nil {
		return nil, err
	}
	return json.Marshal(LedgerListResponse{Entries: entries})
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
