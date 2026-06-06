// `satellites evidence` — read + record the durable QA-evidence trail
// (sty_7d2e9847, epic:qa-observability). Gate runs capture their own evidence
// automatically (cmd_story_review records a `gate` row per run); this command
// is the out-of-band reader (`show`) and the CI-outcome recorder (`ci`).
//
// `evidence show <story>` lists a story's captured QA trail from the per-repo
// store (state.db) — the read-source the order:9 audit + QA view consume. It
// never enters the executor's turn (the epic anti-goal).
//
// `evidence ci <story>` records a CI stage outcome: a durable `ci` row in the
// store AND a `ci_result` row on the server ledger via the existing
// ledger_append verb (the spike's "CI-outcome ledger row" — no new MCP verb), so
// a story's full QA trail (gates + CI) is reconstructable after the fact.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/cliconfig"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workstate"
	"github.com/spf13/cobra"
)

// validCIStages / validCIResults keep the recorded vocabulary closed so the
// audit (order:9) can match on stable values rather than free text.
var (
	validCIStages  = map[string]bool{"test": true, "release": true, "deploy": true}
	validCIResults = map[string]bool{"success": true, "failure": true}
)

func init() {
	ev := &cobra.Command{
		Use:   "evidence",
		Short: "Read + record the durable QA-evidence trail (gate runs + CI outcomes) per story",
		Long: `evidence is the out-of-band QA trail for a story: the gate runs captured
automatically by the reviewer loop and the CI outcomes recorded here. It reads
from / writes to the per-repo store (.satellites/work/state.db) and, for CI, the
server ledger — never the executor's turn.`,
	}

	var showConfig string
	var showJSON bool
	showCmd := &cobra.Command{
		Use:   "show <story-id>",
		Short: "List a story's captured QA evidence (gate runs + CI outcomes)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEvidenceShow(cmd.OutOrStdout(), resolveStateDB(showConfig), strings.TrimSpace(args[0]), showJSON)
		},
	}
	showCmd.Flags().StringVar(&showConfig, "config", "", "Path to satellites.toml (resolves repo root + state_db; defaults to walk-up from CWD).")
	showCmd.Flags().BoolVar(&showJSON, "json", false, "Emit the evidence rows as JSON.")
	ev.AddCommand(showCmd)

	var ciConfig, ciUser, ciStage, ciResult, ciRef, ciNotes string
	ciCmd := &cobra.Command{
		Use:   "ci <story-id> --stage <test|release|deploy> --result <success|failure>",
		Short: "Record a CI stage outcome against a story (store + ci_result ledger row)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			stage, result := strings.TrimSpace(ciStage), strings.TrimSpace(ciResult)
			if !validCIStages[stage] {
				return fmt.Errorf("--stage must be one of test|release|deploy (got %q)", stage)
			}
			if !validCIResults[result] {
				return fmt.Errorf("--result must be one of success|failure (got %q)", result)
			}
			cfg, _, _ := cliconfig.Load(ciConfig)
			appendLedger := func(ctx context.Context, req json.RawMessage) error {
				_, err := dispatchVerb(ctx, "ledger_append", req, ciConfig, ciUser)
				return err
			}
			return runEvidenceCI(cmd.Context(), cmd.OutOrStdout(), evidenceCIOpts{
				StateDB:   resolveStateDB(ciConfig),
				Story:     strings.TrimSpace(args[0]),
				Stage:     stage,
				Result:    result,
				Ref:       strings.TrimSpace(ciRef),
				Notes:     strings.TrimSpace(ciNotes),
				ProjectID: cfg.ProjectID,
			}, appendLedger)
		},
	}
	ciCmd.Flags().StringVar(&ciConfig, "config", "", "Path to satellites.toml (defaults to walk-up from CWD).")
	ciCmd.Flags().StringVar(&ciUser, "user", "", "Caller user id (defaults to the configured admin user).")
	ciCmd.Flags().StringVar(&ciStage, "stage", "", "REQUIRED. CI stage: test | release | deploy.")
	ciCmd.Flags().StringVar(&ciResult, "result", "", "REQUIRED. Outcome: success | failure.")
	ciCmd.Flags().StringVar(&ciRef, "ref", "", "Commit sha or run URL the outcome is for.")
	ciCmd.Flags().StringVar(&ciNotes, "notes", "", "Optional detail line.")
	ev.AddCommand(ciCmd)

	register(ev)
}

// runEvidenceShow lists a story's QA-evidence rows from the store.
func runEvidenceShow(out io.Writer, stateDB, story string, asJSON bool) error {
	if story == "" {
		return fmt.Errorf("story id required")
	}
	store, err := workstate.Open(stateDB)
	if err != nil {
		return fmt.Errorf("evidence show: open store: %w", err)
	}
	defer store.Close()
	rows, err := store.ListEvidence(story)
	if err != nil {
		return fmt.Errorf("evidence show: %w", err)
	}
	if asJSON {
		return json.NewEncoder(out).Encode(evidenceJSON(rows))
	}
	if len(rows) == 0 {
		fmt.Fprintf(out, "no captured evidence for %s\n", story)
		return nil
	}
	for _, r := range rows {
		ts := r.TS.Format(time.RFC3339)
		switch r.Kind {
		case workstate.EvidenceGate:
			line := fmt.Sprintf("%s  gate  %s  %s", ts, r.Label, r.Decision)
			if r.FromStatus != "" || r.ToStatus != "" {
				line += fmt.Sprintf("  (%s→%s)", r.FromStatus, r.ToStatus)
			}
			fmt.Fprintln(out, line)
		case workstate.EvidenceCI:
			line := fmt.Sprintf("%s  ci    %s  %s", ts, r.Label, r.Decision)
			if r.Ref != "" {
				line += "  ref=" + r.Ref
			}
			fmt.Fprintln(out, line)
		default:
			fmt.Fprintf(out, "%s  %s  %s  %s\n", ts, r.Kind, r.Label, r.Decision)
		}
		if strings.TrimSpace(r.Notes) != "" {
			fmt.Fprintf(out, "        %s\n", r.Notes)
		}
	}
	return nil
}

type evidenceCIOpts struct {
	StateDB   string
	Story     string
	Stage     string
	Result    string
	Ref       string
	Notes     string
	ProjectID string
}

// runEvidenceCI records a CI outcome both in the store (the local trail) and on
// the server ledger as a ci_result row (durable, audit-visible). A ledger-append
// failure warns but does not lose the local capture — the server stays the
// authority and the row can be re-flushed. appendLedger is injected so the core
// is testable without a server.
func runEvidenceCI(ctx context.Context, out io.Writer, opts evidenceCIOpts, appendLedger func(context.Context, json.RawMessage) error) error {
	if opts.Story == "" {
		return fmt.Errorf("story id required")
	}
	store, err := workstate.Open(opts.StateDB)
	if err != nil {
		return fmt.Errorf("evidence ci: open store: %w", err)
	}
	defer store.Close()
	if _, err := store.RecordEvidence(workstate.Evidence{
		Story:    opts.Story,
		Kind:     workstate.EvidenceCI,
		Label:    opts.Stage,
		Decision: opts.Result,
		Notes:    opts.Notes,
		Ref:      opts.Ref,
		TS:       time.Now(),
	}); err != nil {
		return fmt.Errorf("evidence ci: record: %w", err)
	}

	// Mirror to the server ledger so the CI outcome is durable + audit-visible
	// (the spike's "CI-outcome ledger row"; existing verb, no new MCP verb).
	payload, _ := json.Marshal(map[string]any{"stage": opts.Stage, "result": opts.Result, "ref": opts.Ref})
	req, mErr := json.Marshal(verb.LedgerAppendRequest{
		StoryID:   opts.Story,
		ProjectID: opts.ProjectID,
		Kind:      "ci_result",
		Body:      fmt.Sprintf("ci %s: %s", opts.Stage, opts.Result),
		Payload:   payload,
	})
	if mErr == nil && appendLedger != nil {
		if lErr := appendLedger(ctx, req); lErr != nil {
			fmt.Fprintf(out, "warn: ledger_append ci_result failed (captured locally, retry later): %v\n", lErr)
		}
	}
	fmt.Fprintf(out, "recorded ci %s=%s for %s\n", opts.Stage, opts.Result, opts.Story)
	return nil
}

// evidenceRow is the JSON projection of one captured row.
type evidenceRow struct {
	Seq        int64  `json:"seq"`
	Story      string `json:"story"`
	Kind       string `json:"kind"`
	Label      string `json:"label"`
	Decision   string `json:"decision"`
	Notes      string `json:"notes,omitempty"`
	FromStatus string `json:"from_status,omitempty"`
	ToStatus   string `json:"to_status,omitempty"`
	Ref        string `json:"ref,omitempty"`
	TS         string `json:"ts"`
}

func evidenceJSON(rows []workstate.Evidence) []evidenceRow {
	out := make([]evidenceRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, evidenceRow{
			Seq: r.Seq, Story: r.Story, Kind: r.Kind, Label: r.Label,
			Decision: r.Decision, Notes: r.Notes, FromStatus: r.FromStatus,
			ToStatus: r.ToStatus, Ref: r.Ref, TS: r.TS.Format(time.RFC3339),
		})
	}
	return out
}
