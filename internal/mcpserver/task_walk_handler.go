package mcpserver

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/task"
)

// taskWalkResponse is the wire shape for task_walk. Field order matches
// the AC in sty_41488515 — story metadata header, ordered CI list,
// current pointer, and the configuration the workflow was composed
// against. sty_41488515.
type taskWalkResponse struct {
	Story             taskWalkStory `json:"story"`
	ContractInstances []taskWalkCI  `json:"contract_instances"`
	CurrentCIID       string        `json:"current_ci_id,omitempty"`
	ConfigurationName string        `json:"configuration_name,omitempty"`
}

type taskWalkStory struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

type taskWalkCI struct {
	ID               string             `json:"id"`
	ContractName     string             `json:"contract_name"`
	ContractCategory string             `json:"contract_category,omitempty"`
	Sequence         int                `json:"sequence"`
	Iteration        int                `json:"iteration"`
	Status           string             `json:"status"`
	ClaimedBySession string             `json:"claimed_by_session,omitempty"`
	ClaimedByUser    string             `json:"claimed_by_user,omitempty"`
	ClaimedAt        *time.Time         `json:"claimed_at,omitempty"`
	ClosedAt         *time.Time         `json:"closed_at,omitempty"`
	Outcome          string             `json:"outcome,omitempty"`
	PlanLedgerID     string             `json:"plan_ledger_id,omitempty"`
	CloseLedgerID    string             `json:"close_ledger_id,omitempty"`
	LedgerRowCount   int                `json:"ledger_row_count"`
	TaskSummary      taskWalkTaskCounts `json:"task_summary"`
}

type taskWalkTaskCounts struct {
	Enqueued      int `json:"enqueued"`
	InFlight      int `json:"in_flight"`
	ClosedSuccess int `json:"closed_success"`
	ClosedFailure int `json:"closed_failure"`
	ClosedTimeout int `json:"closed_timeout"`
}

// handleTaskWalk implements the task_walk MCP verb: returns a single
// coherent payload describing where a story sits in its contract walk.
// Replaces the agent-side stitch of story_get + contract_next +
// task_list. sty_41488515.
func (s *Server) handleTaskWalk(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	if s.stories == nil || s.contracts == nil {
		return mcpgo.NewToolResultError("task_walk unavailable: story or contract store missing"), nil
	}
	storyID, err := req.RequireString("story_id")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	caller, _ := UserFrom(ctx)
	memberships := s.resolveCallerMemberships(ctx, caller)
	resp, err := s.buildTaskWalk(ctx, storyID, memberships)
	if err != nil {
		body, _ := json.Marshal(map[string]any{"error": err.Error()})
		return mcpgo.NewToolResultError(string(body)), nil
	}
	body, _ := json.Marshal(resp)
	return mcpgo.NewToolResultText(string(body)), nil
}

// buildTaskWalk projects the story's contract walk into the wire shape.
// Shared by handleTaskWalk and handleStoryExportWalk so both surfaces
// see the same data without re-implementing the projection.
func (s *Server) buildTaskWalk(ctx context.Context, storyID string, memberships []string) (taskWalkResponse, error) {
	st, err := s.stories.GetByID(ctx, storyID, memberships)
	if err != nil {
		return taskWalkResponse{}, errStoryNotFound
	}
	cis, err := s.contracts.List(ctx, storyID, memberships)
	if err != nil {
		return taskWalkResponse{}, err
	}

	resp := taskWalkResponse{
		Story: taskWalkStory{
			ID:     st.ID,
			Title:  st.Title,
			Status: st.Status,
		},
		ContractInstances: make([]taskWalkCI, 0, len(cis)),
	}

	// Pre-load ledger rows + tasks scoped to the story project so each
	// CI can compute its row count + task summary without per-CI list
	// roundtrips. ledger.List is project-scoped; tasks have no story
	// filter so we list per-CI.
	var ledgerByCI map[string]int
	if s.ledger != nil && st.ProjectID != "" {
		rows, lerr := s.ledger.List(ctx, st.ProjectID, ledger.ListOptions{StoryID: storyID, Limit: ledger.MaxListLimit}, memberships)
		if lerr == nil {
			ledgerByCI = make(map[string]int, len(cis))
			for _, r := range rows {
				if r.ContractID == nil {
					continue
				}
				ledgerByCI[*r.ContractID]++
			}
		}
	}

	contractDocCache := map[string]document.Document{}
	resolveContractDoc := func(id string) document.Document {
		if id == "" || s.docs == nil {
			return document.Document{}
		}
		if cached, ok := contractDocCache[id]; ok {
			return cached
		}
		doc, err := s.docs.GetByID(ctx, id, nil)
		if err != nil {
			return document.Document{}
		}
		contractDocCache[id] = doc
		return doc
	}

	currentCIID := ""
	for _, ci := range cis {
		iteration := iterationFromPeers(ci, cis)
		entry := taskWalkCI{
			ID:             ci.ID,
			ContractName:   ci.ContractName,
			Sequence:       ci.Sequence,
			Iteration:      iteration,
			Status:         ci.Status,
			PlanLedgerID:   ci.PlanLedgerID,
			CloseLedgerID:  ci.CloseLedgerID,
			LedgerRowCount: ledgerByCI[ci.ID],
			TaskSummary:    taskSummaryForCI(ctx, s.tasks, ci.ID, memberships),
		}
		if !ci.ClaimedAt.IsZero() {
			ca := ci.ClaimedAt
			entry.ClaimedAt = &ca
		}
		// closed_at is the UpdatedAt timestamp once the CI is in a
		// terminal state; pre-terminal rows leave it nil.
		switch ci.Status {
		case contract.StatusPassed, contract.StatusFailed, contract.StatusSkipped:
			ua := ci.UpdatedAt
			entry.ClosedAt = &ua
			entry.Outcome = ci.Status
		}
		// Resolve contract doc for category.
		doc := resolveContractDoc(ci.ContractID)
		entry.ContractCategory = extractStructuredString(doc.Structured, "category")
		entry.ClaimedByUser = lookupActionClaimUser(ctx, s.ledger, st.ProjectID, ci.ID, memberships)

		// Track the first non-terminal CI as the current pointer. Walk
		// is in workflow order (sequence ASC) so the first match wins.
		if currentCIID == "" {
			switch ci.Status {
			case contract.StatusPassed, contract.StatusFailed, contract.StatusSkipped:
				// terminal — keep scanning
			default:
				currentCIID = ci.ID
			}
		}
		resp.ContractInstances = append(resp.ContractInstances, entry)
	}
	resp.CurrentCIID = currentCIID
	return resp, nil
}

// errStoryNotFound is the sentinel buildTaskWalk returns when the
// story id does not resolve in the caller's workspaces. Callers
// translate it to the json error body shape used by their handler.
var errStoryNotFound = errStr("story_not_found")

type errStr string

func (e errStr) Error() string { return string(e) }

// iterationFromPeers counts CIs of the same contract_name on the same
// story whose CreatedAt is at or before ci's. Returns 1 when no peer
// is found (defensive default).
func iterationFromPeers(ci contract.ContractInstance, peers []contract.ContractInstance) int {
	n := 0
	for _, p := range peers {
		if p.ContractName != ci.ContractName {
			continue
		}
		if p.CreatedAt.After(ci.CreatedAt) {
			continue
		}
		n++
	}
	if n == 0 {
		return 1
	}
	return n
}

// taskSummaryForCI buckets tasks scoped to ciID by status/outcome.
// Returns zeros when the task store is unwired.
func taskSummaryForCI(ctx context.Context, tasks task.Store, ciID string, memberships []string) taskWalkTaskCounts {
	out := taskWalkTaskCounts{}
	if tasks == nil || ciID == "" {
		return out
	}
	rows, err := tasks.List(ctx, task.ListOptions{ContractInstanceID: ciID, Limit: 500}, memberships)
	if err != nil {
		return out
	}
	for _, t := range rows {
		switch t.Status {
		case task.StatusEnqueued:
			out.Enqueued++
		case task.StatusClaimed, task.StatusInFlight:
			out.InFlight++
		case task.StatusClosed:
			switch t.Outcome {
			case task.OutcomeSuccess:
				out.ClosedSuccess++
			case task.OutcomeFailure:
				out.ClosedFailure++
			case task.OutcomeTimeout:
				out.ClosedTimeout++
			}
		}
	}
	return out
}

// lookupActionClaimUser returns the CreatedBy of the latest action_claim
// row scoped to ciID, or "" when nothing matches. Used to populate the
// claimed_by_user response field — the role-grant table does not carry
// the user id directly so the ledger row is the authoritative source.
func lookupActionClaimUser(ctx context.Context, led ledger.Store, projectID, ciID string, memberships []string) string {
	if led == nil || projectID == "" || ciID == "" {
		return ""
	}
	rows, err := led.List(ctx, projectID, ledger.ListOptions{
		Type:       ledger.TypeActionClaim,
		ContractID: ciID,
		Limit:      32,
	}, memberships)
	if err != nil || len(rows) == 0 {
		return ""
	}
	// Newest first — pick the most recent.
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].CreatedAt.After(rows[j].CreatedAt)
	})
	return rows[0].CreatedBy
}

// extractStructuredString reads a single string field from a JSON object
// payload. Returns "" when the payload is empty, malformed, or the key
// is absent / non-string.
func extractStructuredString(raw []byte, key string) string {
	if len(raw) == 0 || key == "" {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	if v, ok := obj[key].(string); ok {
		return v
	}
	return ""
}
