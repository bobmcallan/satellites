// `satellites story set-status` — a status change made directly, without a
// reviewer gate, for the two cases that have no gated edge (sty_4f5148c4 +
// epic:satellites-backbone 1.1):
//
//   - reopening a PARENT/epic (e.g. done → backlog), the original operator
//     override; and
//   - advancing a story along an UNGATED edge of its governing workflow — the
//     gateless-baseline path (backlog → in_progress → done), where the move is
//     enacted directly and the goal-keeper, not a gate, holds the agent to done.
//
// It records a status_transition ledger row under the operator's admin auth (the
// same authority `story status_transition` uses) — NOT a document_upsert, so it
// isn't subject to (and doesn't reopen) the status-write gating. MCP cannot do
// this at all; the portal and this client can. A move across a GATED edge is
// still refused — regular gated work goes through the reviewer gate, so the
// gates stay the only path for the work they govern.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/spf13/cobra"
)

func newStorySetStatusCmd(configArg, userArg *string) *cobra.Command {
	return &cobra.Command{
		Use:   "set-status <story-id> <status>",
		Short: "Set a status directly across an ungated edge: a PARENT/epic reopen, or a gateless-workflow advance",
		Long: `set-status moves a story to <status> by recording a status_transition
ledger row under the operator's admin auth, for the moves that have no gated
edge: reopening a parent (epic) story (e.g. done → backlog), and advancing a
story along an UNGATED edge of its governing workflow (the gateless baseline's
backlog → in_progress → done). A move across a GATED edge is refused — that work
goes through the reviewer gate. Status writes over MCP are blocked, so this
client (or the portal) is the sanctioned surface.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStorySetStatus(cmd.Context(), cmd.OutOrStdout(), *configArg, *userArg, args[0], args[1])
		},
	}
}

func runStorySetStatus(ctx context.Context, out io.Writer, configPath, userArg, storyID, status string) error {
	// Resolve the story and refuse anything that isn't a parent — this override
	// is for epics only; regular stories go through the gate.
	getReq, err := json.Marshal(verb.DocumentGetRequest{ID: storyID})
	if err != nil {
		return err
	}
	raw, err := dispatchVerb(ctx, "document_get", getReq, configPath, userArg)
	if err != nil {
		return fmt.Errorf("set-status: resolve %s: %w", storyID, err)
	}
	var resp verb.DocumentGetResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("set-status: decode %s: %w", storyID, err)
	}
	d := resp.Document
	body := resp.RawBody
	if body == "" && len(resp.Versions) > 0 {
		body = resp.Versions[len(resp.Versions)-1].Body
	}

	parent := d.Category == "parent"
	if !setStatusAllowed(d.Category, d.Status, status, body, governingWorkflowSources(configPath)) {
		return fmt.Errorf("set-status: %s (category=%q, status=%q) has no ungated %s→%s edge — a gated move goes through the reviewer gate (satellites story status_transition --skill <gate>); set-status is for a parent reopen or a gateless-workflow advance", storyID, d.Category, d.Status, d.Status, status)
	}

	req, err := json.Marshal(setStatusLedgerRequest(storyID, status))
	if err != nil {
		return err
	}
	if _, err := dispatchVerb(ctx, "ledger_append", req, configPath, userArg); err != nil {
		return fmt.Errorf("set-status: %w", err)
	}
	how := "gateless advance"
	if parent {
		how = "parent operator override"
	}
	fmt.Fprintf(out, "set %s → %s (%s)\n", storyID, status, how)
	return nil
}

// setStatusAllowed decides whether a direct (un-gated) set-status move is
// sanctioned: a parent reopen (the operator override) OR a gateless-baseline
// advance along an ungated edge of the governing workflow. A move across a gated
// edge — or one the workflow does not declare — is refused. Pure for tests.
func setStatusAllowed(category, status, target, body string, sources []verb.WorkflowSource) bool {
	if category == "parent" {
		return true
	}
	return verb.GoverningUngatedAdvance(body, status, category, target, sources)
}

// requireParent rejects a non-parent story — pure for tests.
func requireParent(category, storyID string) error {
	if category != "parent" {
		return fmt.Errorf("set-status: %s is category=%q; this operator override is for category:parent (epics) only — regular stories move through the reviewer gate", storyID, category)
	}
	return nil
}

// setStatusLedgerRequest builds the status_transition ledger row that projects
// onto the story's status — pure for tests.
func setStatusLedgerRequest(storyID, status string) verb.LedgerAppendRequest {
	payload, _ := json.Marshal(map[string]any{"to_status": status})
	return verb.LedgerAppendRequest{
		StoryID: storyID,
		Kind:    "status_transition",
		Body:    fmt.Sprintf("operator set-status → %s", status),
		Payload: payload,
	}
}
