// `satellites ledger` — server-side reads over the evidence ledger
// (sty_eb4876e9). Before this command the ledger had no read path outside the
// portal: story history (gate verdicts, transitions, summaries) required the
// portal UI. `ledger list <story-id>` prints the recent rows, newest last, so
// an agent or operator can see a story's server history from the worktree.
// Read-only; appends stay with the gate chain and `ledger_append`.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/spf13/cobra"
)

func init() {
	var (
		configArg string
		userArg   string
	)
	ledgerCmd := &cobra.Command{
		Use:     "ledger",
		Aliases: []string{"ledgers"},
		Short:   "Read the server evidence ledger (story history: transitions, gate verdicts, summaries)",
	}
	ledgerCmd.PersistentFlags().StringVar(&configArg, "config", "", "Path to satellites.toml (overrides $SATELLITES_CONFIG / .satellites/satellites.toml walk-up).")
	ledgerCmd.PersistentFlags().StringVar(&userArg, "user", "", "Caller user id (overrides $SATELLITES_USER_ID). Stamped onto verbs when dispatching in-process.")
	ledgerCmd.AddCommand(newLedgerListCmd(&configArg, &userArg))
	register(ledgerCmd)
}

func newLedgerListCmd(configArg, userArg *string) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "list <story-id>",
		Short: "List a story's recent server ledger rows (newest last)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLedgerList(cmd.Context(), cmd.OutOrStdout(), *configArg, *userArg, args[0], limit)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum rows to print (the most recent)")
	return cmd
}

func runLedgerList(ctx context.Context, out io.Writer, configPath, userArg, storyID string, limit int) error {
	if limit <= 0 {
		limit = 20
	}
	// Page oldest-first to the tail, keeping only the formatted last N — the
	// row slice flows by inference and the CLI never names internal/ledger.
	var lines []string
	cursor := ""
	for {
		req, err := json.Marshal(verb.LedgerListRequest{StoryID: storyID, Limit: 200, Cursor: cursor})
		if err != nil {
			return err
		}
		raw, err := dispatchVerb(ctx, "ledger_list", req, configPath, userArg)
		if err != nil {
			return fmt.Errorf("ledger list: %s: %w", storyID, err)
		}
		var resp verb.LedgerListResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return fmt.Errorf("ledger list: decode: %w", err)
		}
		for _, e := range resp.Entries {
			lines = append(lines, formatLedgerRow(e.CreatedAt, e.Kind, e.Actor, e.Body, e.Payload))
		}
		if len(lines) > limit {
			lines = lines[len(lines)-limit:]
		}
		if resp.NextCursor == "" {
			break
		}
		cursor = resp.NextCursor
	}
	if len(lines) == 0 {
		fmt.Fprintf(out, "no ledger rows for %s\n", storyID)
		return nil
	}
	for _, l := range lines {
		fmt.Fprintln(out, l)
	}
	return nil
}

// formatLedgerRow renders one ledger row — pure for tests. A
// status_transition payload is annotated as "from → to"; other kinds show
// their body's first line.
func formatLedgerRow(createdAt time.Time, kind, actor, body string, payload json.RawMessage) string {
	note := firstLine(body)
	if kind == "status_transition" {
		if from, to := transitionStatuses(payload); to != "" {
			arrow := fmt.Sprintf("%s → %s", dashIfEmpty(from), to)
			if note == arrow {
				note = "" // body merely restates the transition
			}
			note = strings.TrimSpace(arrow + "  " + note)
		}
	}
	return fmt.Sprintf("%s  %-18s %-10s %s",
		createdAt.UTC().Format("2006-01-02 15:04:05"), kind, dashIfEmpty(actor), note)
}

// transitionStatuses pulls the from/to statuses out of a status_transition
// payload; empty strings when absent or unparsable.
func transitionStatuses(payload json.RawMessage) (string, string) {
	if len(payload) == 0 {
		return "", ""
	}
	var p struct {
		FromStatus string `json:"from_status"`
		ToStatus   string `json:"to_status"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return "", ""
	}
	return strings.TrimSpace(p.FromStatus), strings.TrimSpace(p.ToStatus)
}
