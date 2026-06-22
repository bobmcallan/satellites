// `satellites context repair` — the report→repair loop (epic:context-validation,
// parent sty_e29dc6ff, child sty_fca2a8a0). It reuses the SAME corpus findings
// `context validate` produces (gatherCorpusFindings — no second validator),
// classifies each into auto/expected/manual, and on --apply drives the EXISTING
// repair mechanisms to fix the auto ones, then re-validates to show the delta.
//
// Config-over-code (satellites-constitution): repair ROUTES + RUNS, it bakes in
// no fix decision. The auto lever for a workflow finding is runWorkflowEmbed,
// which resolves the governing workflow FROM THE SUBSTRATE by category — the
// decision stays in the substrate. manual findings name the system skill the
// operator runs (corpus_review / curate / authoring).
//
// Operator-facing + out of band: writes NO ledger rows; never a gate the
// working agent runs. Corpus-mutating --apply is opt-in only.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// repairKind classifies what can be done about a finding.
type repairKind string

const (
	repairAuto     repairKind = "auto"     // a deterministic existing lever applies it
	repairExpected repairKind = "expected" // not a defect (e.g. pre-start story has no embedded workflow yet)
	repairManual   repairKind = "manual"   // needs authoring judgment; names the system skill/command
)

// repairAction is the planned (and, on --apply, executed) remediation for one
// finding.
type repairAction struct {
	StoryID   string     `json:"story_id,omitempty"`
	Code      string     `json:"code"`
	Kind      repairKind `json:"kind"`
	Command   string     `json:"command,omitempty"`
	Rationale string     `json:"rationale"`
	Applied   bool       `json:"applied"`
	Error     string     `json:"error,omitempty"`
}

// repairReport is the structured plan/result (the --json shape).
type repairReport struct {
	FindingsBefore int            `json:"findings_before"`
	Actions        []repairAction `json:"actions"`
	FindingsAfter  *int           `json:"findings_after,omitempty"`
}

func newContextRepairCmd(configArg, userArg *string) *cobra.Command {
	var (
		apply  bool
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "repair",
		Short: "Drive the existing system mechanisms to apply the corpus validation report's fix actions",
		Long: `repair reuses the corpus findings 'context validate' produces and, for each,
plans a remediation:

  auto      a deterministic existing lever applies it (a started story's missing
            or broken ## Workflow is stamped by 'workflow embed').
  expected  not a defect — a pre-start story has no embedded ## Workflow yet; it
            is stamped at start. Reported, never auto-applied.
  manual    needs authoring judgment — names the system skill/command to run
            (satellites skill sync, corpus_review, context curate).

Default mode is a DRY-RUN that prints the plan and applies nothing. --apply
executes only the auto actions (reusing the existing repair functions), then
re-validates and reports the before→after finding counts. It writes no ledger
rows; --apply mutates the corpus and is opt-in only.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return runContextRepair(ctx, contextRepairOpts{
				ConfigPath: *configArg,
				UserArg:    *userArg,
				StateDB:    resolveStateDB(*configArg),
				Apply:      apply,
				JSON:       asJSON,
			}, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "Execute the auto repair actions (mutates the corpus); default is a dry-run plan.")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Emit the repair plan/result as JSON.")
	return cmd
}

type contextRepairOpts struct {
	ConfigPath string
	UserArg    string
	StateDB    string
	Apply      bool
	JSON       bool
}

func (o contextRepairOpts) validateOpts() contextValidateOpts {
	return contextValidateOpts{ConfigPath: o.ConfigPath, UserArg: o.UserArg, StateDB: o.StateDB}
}

// startedStatus reports whether a story has begun execution — past the point
// where its ## Workflow should be embedded. A pre-start story legitimately has
// none yet (it is stamped at start), so a workflow finding there is expected.
//
// NOTE (sty_9f97ff5c): deliberately kept as a literal allowlist. Migrating it to
// the governing workflow's IsEditable needs FULL-source (on-disk project workflow)
// resolution to recognise repo-specific WORKING states (e.g. blocked / shipping)
// that the embed-only classifier cannot see — and threading that resolution
// breaks this planner's pure unit-testability. Deferred: this is a
// repair-classification heuristic, not a gate.
func startedStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "in_progress", "techdebt-review", "integration-review", "done-review", "blocked":
		return true
	}
	return false
}

// planRepair classifies one finding into an actionable plan. Pure over (finding,
// status) so it unit-tests directly; the fix DECISION it routes to lives in the
// substrate-driven levers, never here.
func planRepair(f validateFinding, status string) repairAction {
	a := repairAction{StoryID: f.StoryID, Code: f.Code}
	switch f.Code {
	case "workflow-unparseable", "workflow-lifecycle":
		if startedStatus(status) {
			a.Kind = repairAuto
			a.Command = "workflow embed " + f.StoryID
			a.Rationale = "started story missing/broken ## Workflow — stamp the governing workflow (runWorkflowEmbed)"
		} else {
			a.Kind = repairExpected
			a.Rationale = "pre-start story; its ## Workflow is stamped at start by `workflow embed` — not a defect"
		}
	case "missing-gate-skill":
		a.Kind = repairManual
		a.Command = "satellites skill sync"
		a.Rationale = "materialise the named gate skill (or author it and `satellites skill upload`)"
	default:
		a.Kind = repairManual
		a.Rationale = "reconcile via the system authoring/review skills (corpus_review / context curate)"
	}
	return a
}

// repairSummary is the auto/expected/manual/applied tally — the before→after
// accounting (AC4). Pure for direct testing.
type repairSummary struct {
	Auto     int
	Expected int
	Manual   int
	Applied  int
}

func summarizeRepair(actions []repairAction) repairSummary {
	var s repairSummary
	for _, a := range actions {
		switch a.Kind {
		case repairAuto:
			s.Auto++
		case repairExpected:
			s.Expected++
		case repairManual:
			s.Manual++
		}
		if a.Applied {
			s.Applied++
		}
	}
	return s
}

func runContextRepair(ctx context.Context, opts contextRepairOpts, stdout, stderr io.Writer) error {
	before, stories, err := gatherCorpusFindings(ctx, opts.validateOpts(), stderr)
	if err != nil {
		return err
	}
	statusByStory := map[string]string{}
	for _, s := range stories {
		statusByStory[s.ID] = s.Status
	}

	report := repairReport{FindingsBefore: len(before.Findings)}
	for _, f := range before.Findings {
		report.Actions = append(report.Actions, planRepair(f, statusByStory[f.StoryID]))
	}

	if opts.Apply {
		applyAutoRepairs(ctx, opts, report.Actions, stderr)
		if rep, _, rerr := gatherCorpusFindings(ctx, opts.validateOpts(), stderr); rerr == nil {
			n := len(rep.Findings)
			report.FindingsAfter = &n
		}
	}

	if opts.JSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	renderRepairReport(stdout, report, opts.Apply)
	return nil
}

// applyAutoRepairs executes the auto actions in place (mutating the action
// rows). Each story is embedded at most once per run — reusing runWorkflowEmbed,
// never re-implementing workflow stamping. io.Discard swallows the per-embed
// stdout; failures are recorded on the action, never fatal.
func applyAutoRepairs(ctx context.Context, opts contextRepairOpts, actions []repairAction, stderr io.Writer) {
	embedded := map[string]bool{}  // story -> success
	attempted := map[string]bool{} // story -> tried
	for i := range actions {
		if actions[i].Kind != repairAuto || actions[i].StoryID == "" {
			continue
		}
		sid := actions[i].StoryID
		if !attempted[sid] {
			attempted[sid] = true
			if err := runWorkflowEmbed(ctx, io.Discard, opts.ConfigPath, opts.UserArg, sid, false); err != nil {
				fmt.Fprintf(stderr, "warn: context repair: workflow embed %s: %v\n", sid, err)
				actions[i].Error = err.Error()
			} else {
				embedded[sid] = true
			}
		}
		actions[i].Applied = embedded[sid]
		if !actions[i].Applied && actions[i].Error == "" {
			actions[i].Error = "workflow embed failed for this story (see the first action for it)"
		}
	}
}

func renderRepairReport(w io.Writer, r repairReport, applied bool) {
	s := summarizeRepair(r.Actions)
	mode := "DRY-RUN"
	if applied {
		mode = "APPLIED"
	}
	fmt.Fprintf(w, "context repair (%s): %d finding(s) — %d auto, %d expected, %d manual\n",
		mode, r.FindingsBefore, s.Auto, s.Expected, s.Manual)
	if len(r.Actions) > 0 {
		fmt.Fprintln(w)
		tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "KIND\tCODE\tSTORY\tAPPLIED\tACTION")
		for _, a := range r.Actions {
			story := a.StoryID
			if story == "" {
				story = "-"
			}
			action := a.Rationale
			if a.Command != "" {
				action = a.Command + " — " + a.Rationale
			}
			ap := "-"
			if applied && a.Kind == repairAuto {
				if a.Applied {
					ap = "yes"
				} else {
					ap = "FAILED"
				}
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", a.Kind, a.Code, story, ap, action)
		}
		tw.Flush()
	}
	if applied {
		if r.FindingsAfter != nil {
			fmt.Fprintf(w, "\nbefore → after: %d → %d finding(s) (%d applied)\n", r.FindingsBefore, *r.FindingsAfter, s.Applied)
		} else {
			fmt.Fprintf(w, "\n%d auto action(s) applied; re-validation unavailable\n", s.Applied)
		}
	} else if s.Auto > 0 {
		fmt.Fprintf(w, "\nrun with --apply to execute the %d auto action(s).\n", s.Auto)
	}
}
