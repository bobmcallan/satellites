// `satellites story set-status` — the operator override for a status change the
// reviewer gate's forward-only workflow can't make (sty_4f5148c4). Its sole
// sanctioned use is reopening a PARENT/epic (e.g. done → backlog), which has no
// gated edge. It records a status_transition ledger row under the operator's
// admin auth (the same authority `story status_transition` uses) — NOT a
// document_upsert, so it isn't subject to (and doesn't reopen) the status-write
// gating. MCP cannot do this at all; the portal and this client can. To keep the
// reviewer gates the only path for regular work, this command refuses any story
// that is not category:parent.

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
		Short: "Operator override: set a PARENT/epic's status directly (e.g. reopen done→backlog)",
		Long: `set-status moves a parent (epic) story to <status> by recording a
status_transition ledger row under the operator's admin auth. It exists for the
one status change the reviewer gate cannot make — reopening a parent (done →
backlog), which has no gated edge. Regular (non-parent) stories are refused: they
move only through the reviewer gate. Status writes over MCP are blocked, so this
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
	if err := requireParent(resp.Document.Category, storyID); err != nil {
		return err
	}

	req, err := json.Marshal(setStatusLedgerRequest(storyID, status))
	if err != nil {
		return err
	}
	if _, err := dispatchVerb(ctx, "ledger_append", req, configPath, userArg); err != nil {
		return fmt.Errorf("set-status: %w", err)
	}
	fmt.Fprintf(out, "set %s → %s (parent operator override)\n", storyID, status)
	return nil
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
