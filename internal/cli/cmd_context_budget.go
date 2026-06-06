// `satellites context budget <story>` — the delivered-context budget ratchet
// (sty_e3e8f3ce, epic:qa-observability). It reuses order:2's
// assembleDeliveredContext to size the ALWAYS-ON executor surface (the bundle
// the spike baselined), records it as a tracked metric on the per-repo store,
// and compares it to the recorded baseline so context GROWTH is visible rather
// than silent. The epic's bet — "the rulebook shrinks against a budget, never
// grows" — needs exactly this number + ratchet.
//
// Operator-facing + out of band (AC4): it reads/records a metric and prints a
// verdict; it is NOT a gate the working agent runs. --strict makes it exit
// non-zero on growth (for a CI/operator check); default warns. No new MCP verb —
// it reuses the order:2 assembly over existing read verbs.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/workstate"
	"github.com/spf13/cobra"
)

// budgetVerdict is the pure ratchet result over a current vs. baseline size.
type budgetVerdict struct {
	CurrentBytes  int  `json:"current_bytes"`
	BaselineBytes int  `json:"baseline_bytes"`
	DeltaBytes    int  `json:"delta_bytes"`
	Grew          bool `json:"grew"`
	HasBaseline   bool `json:"has_baseline"`
}

// evalBudget is the ratchet: growth past baseline flags. A shrink or an exact
// match does not. With no baseline yet it reports HasBaseline=false and never
// flags growth. Pure — directly unit-tested (AC6).
func evalBudget(current, baseline int, hasBaseline bool) budgetVerdict {
	v := budgetVerdict{CurrentBytes: current, HasBaseline: hasBaseline}
	if !hasBaseline {
		return v
	}
	v.BaselineBytes = baseline
	v.DeltaBytes = current - baseline
	v.Grew = current > baseline
	return v
}

func newContextBudgetCmd(configArg, userArg *string) *cobra.Command {
	var (
		setBaseline bool
		strict      bool
		asJSON      bool
	)
	cmd := &cobra.Command{
		Use:   "budget <story-id>",
		Short: "Track + ratchet the always-on delivered-context size against its baseline",
		Long: `budget sizes the ALWAYS-ON executor context (the bundle every session
carries — load-context, tool descriptions, the skills index, the principles
ride-along), reusing the same assembly 'context show' renders, records it as a
tracked metric, and compares it to the recorded baseline so context growth is
visible, not silent.

--set-baseline records the current size as the baseline to ratchet against.
--strict exits non-zero when the size has grown past the baseline (for a CI or
operator check); by default growth only warns. It is an operator signal, never a
gate the working agent runs.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return runContextBudget(ctx, cmd.OutOrStdout(), contextBudgetOpts{
				Story:       strings.TrimSpace(args[0]),
				ConfigPath:  *configArg,
				UserArg:     *userArg,
				StateDB:     resolveStateDB(*configArg),
				SetBaseline: setBaseline,
				Strict:      strict,
				JSON:        asJSON,
			})
		},
	}
	cmd.Flags().BoolVar(&setBaseline, "set-baseline", false, "Record the current always-on size as the baseline to ratchet against.")
	cmd.Flags().BoolVar(&strict, "strict", false, "Exit non-zero when the size has grown past the baseline (default: warn only).")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Emit the measure + verdict as JSON.")
	return cmd
}

type contextBudgetOpts struct {
	Story       string
	ConfigPath  string
	UserArg     string
	StateDB     string
	SetBaseline bool
	Strict      bool
	JSON        bool
}

// alwaysOnSize extracts the always-on partition total + its component breakdown
// from the order:2 assembly — the single tracked number and its parts.
func alwaysOnSize(dc deliveredContext) (bytes int, components []contextComponent) {
	bytes = dc.TotalsBytes[partAlwaysOn]
	for _, c := range dc.Components {
		if c.Partition == partAlwaysOn {
			components = append(components, c)
		}
	}
	sort.SliceStable(components, func(i, j int) bool { return components[i].Bytes > components[j].Bytes })
	return bytes, components
}

func runContextBudget(ctx context.Context, out io.Writer, opts contextBudgetOpts) error {
	if opts.Story == "" {
		return fmt.Errorf("story id required")
	}
	dc, err := assembleDeliveredContext(ctx, opts.Story, opts.ConfigPath, opts.UserArg)
	if err != nil {
		return err
	}
	curBytes, components := alwaysOnSize(dc)
	curTokens := estTokens(curBytes)

	store, err := workstate.Open(opts.StateDB)
	if err != nil {
		return fmt.Errorf("context budget: open store: %w", err)
	}
	defer store.Close()

	baseline, hasBaseline, err := store.ContextBaseline()
	if err != nil {
		return fmt.Errorf("context budget: read baseline: %w", err)
	}

	compJSON, _ := json.Marshal(components)
	if _, err := store.RecordContextSample(workstate.ContextSample{
		Bytes: curBytes, Tokens: curTokens, Components: string(compJSON), TS: time.Now(),
	}, opts.SetBaseline); err != nil {
		return fmt.Errorf("context budget: record sample: %w", err)
	}

	// When set-baseline, the row we just wrote IS the new baseline.
	if opts.SetBaseline {
		baseline = workstate.ContextSample{Bytes: curBytes, Tokens: curTokens}
		hasBaseline = true
	}
	verdict := evalBudget(curBytes, baseline.Bytes, hasBaseline)

	if opts.JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(struct {
			Story       string             `json:"story_id"`
			Components  []contextComponent `json:"always_on_components"`
			TokensEst   int                `json:"always_on_tokens"`
			Verdict     budgetVerdict      `json:"verdict"`
			BaselineSet bool               `json:"baseline_set"`
		}{opts.Story, components, curTokens, verdict, opts.SetBaseline})
	}

	renderContextBudget(out, components, curBytes, curTokens, verdict, opts.SetBaseline)

	if opts.Strict && verdict.Grew {
		return fmt.Errorf("delivered context grew %d bytes past baseline (%d → %d)", verdict.DeltaBytes, verdict.BaselineBytes, verdict.CurrentBytes)
	}
	return nil
}

func renderContextBudget(out io.Writer, components []contextComponent, curBytes, curTokens int, v budgetVerdict, baselineSet bool) {
	fmt.Fprintln(out, "\n── delivered-context budget (always-on executor surface) ──")
	for _, c := range components {
		fmt.Fprintf(out, "  %-34s %7d B  ~%5d tok\n", c.Name, c.Bytes, c.Tokens)
	}
	fmt.Fprintf(out, "  %-34s %7d B  ~%5d tok\n", "TOTAL always-on", curBytes, curTokens)

	switch {
	case baselineSet:
		fmt.Fprintf(out, "\nbaseline set: %d B (~%d tok) — future runs ratchet against this.\n", curBytes, curTokens)
	case !v.HasBaseline:
		fmt.Fprintln(out, "\nno baseline recorded — run with --set-baseline to establish the ratchet point.")
	case v.Grew:
		fmt.Fprintf(out, "\nRATCHET: GREW +%d B past baseline %d B — agent-facing context grew (make it visible, then shrink).\n", v.DeltaBytes, v.BaselineBytes)
	case v.DeltaBytes < 0:
		fmt.Fprintf(out, "\nratchet: OK — shrank %d B below baseline %d B.\n", -v.DeltaBytes, v.BaselineBytes)
	default:
		fmt.Fprintf(out, "\nratchet: OK — at baseline %d B.\n", v.BaselineBytes)
	}
}
