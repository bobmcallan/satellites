// `satellites context validate` — the CORPUS-WIDE context-validation dry-run
// (epic:context-validation, parent sty_e29dc6ff, child sty_08895179). Where
// `context review <story>` checks ONE story's assembled context, this runs the
// SAME checks over the whole substrate at once and emits one consolidated
// report with fix actions.
//
// Pure orchestration, no rebuild (satellites-constitution): it reuses
//   - reviewContextConflicts  — the structural layer, per story's ## Workflow
//   - satellites-context-conflict-review (via semanticConflicts) — the semantic
//     contradiction DECISION, which stays in the substrate skill
//   - alwaysOnSize / evalBudget / the workstate baseline — the size dimension,
//     whose threshold stays the recorded baseline, never a Go constant
// and the workflow-check story-enumeration precedent (resolveDeployScope +
// document_list {type:story} + document_get). No new MCP verb; no new gate.
//
// Operator/CI-facing + out of band: it writes NO ledger rows. --strict exits
// non-zero on any error-severity finding (sibling to `workflow check --strict`
// and `context budget --strict`) — never a gate the working agent runs.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/bobmcallan/satellites/internal/workstate"
	"github.com/spf13/cobra"
)

// validateFinding is one corpus finding. Mirrors conflictFinding (severity/
// code/message) plus the corpus-only fields: which story it came from
// (StoryID, empty for corpus-level findings), a descriptive fix action, and
// the source layer so child-2's repair loop and the operator can tell the
// structural, semantic, and budget streams apart.
type validateFinding struct {
	StoryID  string `json:"story_id,omitempty"`
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Fix      string `json:"fix,omitempty"`
	Source   string `json:"source"`
}

// validateStory is one story's id + status + body — the structural
// aggregator's input, kept minimal so the pure pass is unit-testable without
// dispatch. Status drives child-2's repair classification (started vs not).
type validateStory struct {
	ID     string
	Status string
	Body   string
}

// validateBudget is the always-on size dimension folded into the report —
// reused verbatim from the budget machinery (alwaysOnSize + evalBudget).
type validateBudget struct {
	AlwaysOnBytes  int           `json:"always_on_bytes"`
	AlwaysOnTokens int           `json:"always_on_tokens"`
	Verdict        budgetVerdict `json:"verdict"`
}

// validateReport is the consolidated corpus-wide report (the --json shape).
type validateReport struct {
	StoriesChecked int               `json:"stories_checked"`
	Findings       []validateFinding `json:"findings"`
	Budget         *validateBudget   `json:"budget,omitempty"`
}

func newContextValidateCmd(configArg, userArg *string) *cobra.Command {
	var (
		asJSON    bool
		semantic  bool
		strict    bool
		claudeBin string
	)
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate the WHOLE substrate corpus-wide (structural + size; --semantic adds the LLM contradiction pass)",
		Long: `validate runs the context-review checks over the ENTIRE substrate at once,
rather than one story at a time, and emits a single consolidated report:

  - structural: every non-terminal story's ## Workflow is checked for a
    degenerate lifecycle, a transition naming a non-materialised reviewer_skill,
    or an unparseable block (reusing the context-review structural layer).
  - size: the always-on delivered-context surface is sized and ratcheted against
    the recorded baseline (reusing the context-budget machinery).
  - --semantic: an isolated claude -p reviewer (satellites-context-conflict-review)
    runs ONCE over the corpus bundle (all principles + all skills) and its
    contradiction findings fold into the same stream.

Each finding carries a descriptive fix action. The report applies nothing — it
is an operator/CI signal that writes no ledger rows. --strict exits non-zero on
any error-severity finding.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return runContextValidate(ctx, contextValidateOpts{
				ConfigPath: *configArg,
				UserArg:    *userArg,
				StateDB:    resolveStateDB(*configArg),
				Semantic:   semantic,
				Strict:     strict,
				JSON:       asJSON,
				ClaudeBin:  claudeBin,
			}, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Emit the consolidated report as JSON.")
	cmd.Flags().BoolVar(&semantic, "semantic", false, "Also run the claude -p contradiction reviewer over the corpus bundle.")
	cmd.Flags().BoolVar(&strict, "strict", false, "Exit non-zero when any error-severity finding exists (default: report only).")
	cmd.Flags().StringVar(&claudeBin, "claude-bin", "", "Path to the claude binary for --semantic (defaults to $SATELLITES_CLAUDE_BIN or `claude`).")
	return cmd
}

type contextValidateOpts struct {
	ConfigPath string
	UserArg    string
	StateDB    string
	Semantic   bool
	Strict     bool
	JSON       bool
	ClaudeBin  string
}

// gatherCorpusFindings runs the corpus-wide aggregation — the SINGLE source of
// findings shared by `context validate` and `context repair` (no second
// validator). It resolves the project, enumerates non-terminal stories, folds
// the structural + budget (+ optional semantic) findings, and returns the
// report alongside the stories (repair needs their status). Warnings go to
// stderr; only a hard resolution/listing failure errors.
func gatherCorpusFindings(ctx context.Context, opts contextValidateOpts, stderr io.Writer) (validateReport, []validateStory, error) {
	_, pj, err := resolveDeployScope(ctx, opts.ConfigPath, opts.UserArg)
	if err != nil {
		return validateReport{}, nil, fmt.Errorf("context validate: resolve project: %w", err)
	}
	if strings.TrimSpace(pj) == "" {
		return validateReport{}, nil, fmt.Errorf("context validate: no project configured for this repo (run `satellites project match`?)")
	}

	stories, err := listContextValidateStories(ctx, pj, opts.ConfigPath, opts.UserArg)
	if err != nil {
		return validateReport{}, nil, err
	}

	skillExists := func(name string) bool {
		_, statErr := os.Stat(filepath.Join(".claude", "skills", name, "SKILL.md"))
		return statErr == nil
	}
	report := validateReport{
		StoriesChecked: len(stories),
		Findings:       aggregateStructural(stories, skillExists),
	}

	// Size dimension: the always-on surface is corpus-constant across the
	// project's stories, so size it ONCE off the first story. Best-effort — a
	// failure warns and drops the budget block, never the structural report.
	if len(stories) > 0 {
		if b, berr := corpusBudget(ctx, stories[0].ID, opts); berr != nil {
			fmt.Fprintf(stderr, "warn: context validate budget: %v\n", berr)
		} else {
			report.Budget = b
		}
	}

	// --semantic: the corpus contradiction pass. buildSemanticBundle already
	// reads ALL principles + ALL skills; the per-story workflow slice is
	// irrelevant corpus-wide, so pass an empty one. Failure warns, never fails.
	if opts.Semantic {
		sem, sErr := semanticConflicts(ctx, "", opts.ClaudeBin, stderr)
		if sErr != nil {
			fmt.Fprintf(stderr, "warn: context validate semantic: %v\n", sErr)
		}
		for _, f := range sem {
			report.Findings = append(report.Findings, validateFinding{
				Severity: f.Severity, Code: f.Code, Message: f.Message,
				Fix: mapFix(f.Code), Source: "context-validate-semantic",
			})
		}
	}
	return report, stories, nil
}

func runContextValidate(ctx context.Context, opts contextValidateOpts, stdout, stderr io.Writer) error {
	report, _, err := gatherCorpusFindings(ctx, opts, stderr)
	if err != nil {
		return err
	}

	if opts.JSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else {
		renderValidateReport(stdout, report)
	}

	if opts.Strict && anyErrorFinding(report.Findings) {
		return fmt.Errorf("context validate: %d error-severity finding(s) across the corpus", countErrorFindings(report.Findings))
	}
	return nil
}

// listContextValidateStories enumerates the project's non-terminal stories
// (id + body) — the structural aggregator's input. Mirrors the workflow-check
// listProjectStories precedent: document_list then document_get per id.
func listContextValidateStories(ctx context.Context, projectID, configPath, userArg string) ([]validateStory, error) {
	listReq, _ := json.Marshal(docListRequest{Type: "story", ProjectID: projectID, Limit: 200})
	raw, err := dispatchVerb(ctx, "document_list", listReq, configPath, userArg)
	if err != nil {
		return nil, fmt.Errorf("context validate: list stories: %w", err)
	}
	var listed struct {
		Items []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		return nil, fmt.Errorf("context validate: decode story list: %w", err)
	}
	var out []validateStory
	for _, it := range listed.Items {
		if terminalStoryStatuses[strings.ToLower(it.Status)] {
			continue
		}
		getReq, _ := json.Marshal(struct {
			ID string `json:"id"`
		}{it.ID})
		graw, gErr := dispatchVerb(ctx, "document_get", getReq, configPath, userArg)
		if gErr != nil {
			continue
		}
		var got struct {
			RawBody string `json:"raw_body"`
		}
		if json.Unmarshal(graw, &got) != nil {
			continue
		}
		out = append(out, validateStory{ID: it.ID, Status: it.Status, Body: got.RawBody})
	}
	return out, nil
}

// corpusBudget sizes the always-on surface off one representative story and
// ratchets it against the recorded baseline — read-only, records no sample.
// Reuses assembleDeliveredContext + alwaysOnSize + evalBudget verbatim.
func corpusBudget(ctx context.Context, storyID string, opts contextValidateOpts) (*validateBudget, error) {
	dc, err := assembleDeliveredContext(ctx, storyID, opts.ConfigPath, opts.UserArg)
	if err != nil {
		return nil, err
	}
	curBytes, _ := alwaysOnSize(dc)
	store, err := workstate.Open(opts.StateDB)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	defer store.Close()
	baseline, hasBaseline, err := store.ContextBaseline()
	if err != nil {
		return nil, fmt.Errorf("read baseline: %w", err)
	}
	return &validateBudget{
		AlwaysOnBytes:  curBytes,
		AlwaysOnTokens: estTokens(curBytes),
		Verdict:        evalBudget(curBytes, baseline.Bytes, hasBaseline),
	}, nil
}

// aggregateStructural runs the existing per-story structural check
// (reviewContextConflicts) across every story, tagging each finding with its
// story id and a fix action. Pure over its inputs (mirrors reviewContextConflicts)
// so it unit-tests without dispatch or the filesystem.
func aggregateStructural(stories []validateStory, skillExists func(string) bool) []validateFinding {
	var out []validateFinding
	for _, s := range stories {
		for _, f := range reviewContextConflicts(s.Body, skillExists) {
			out = append(out, validateFinding{
				StoryID:  s.ID,
				Severity: f.Severity,
				Code:     f.Code,
				Message:  f.Message,
				Fix:      mapFix(f.Code),
				Source:   "context-validate",
			})
		}
	}
	return out
}

// mapFix returns a descriptive remediation action for a finding code. Text
// only — this command applies nothing; child-2's repair loop consumes it.
func mapFix(code string) string {
	switch code {
	case "missing-gate-skill":
		return "materialise the named gate skill (`satellites skill sync`), or author it and `satellites skill upload`"
	case "workflow-lifecycle":
		return "repair the story's ## Workflow lifecycle so it has a reachable initial and terminal state"
	case "workflow-unparseable":
		return "fix the story's ## Workflow yaml so it parses"
	default:
		return "reconcile the flagged artifact via the system authoring/review skills"
	}
}

// anyErrorFinding / countErrorFindings drive the --strict exit. Pure.
func anyErrorFinding(findings []validateFinding) bool { return countErrorFindings(findings) > 0 }

func countErrorFindings(findings []validateFinding) int {
	n := 0
	for _, f := range findings {
		if strings.EqualFold(f.Severity, "error") {
			n++
		}
	}
	return n
}

// renderValidateReport prints the consolidated table (or a clean line) plus the
// budget block.
func renderValidateReport(w io.Writer, r validateReport) {
	if len(r.Findings) == 0 {
		fmt.Fprintf(w, "context validate: %d stor%s checked — no findings\n", r.StoriesChecked, plural(r.StoriesChecked))
	} else {
		fmt.Fprintf(w, "context validate: %d stor%s checked — %d finding(s)\n\n", r.StoriesChecked, plural(r.StoriesChecked), len(r.Findings))
		tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "SEVERITY\tCODE\tSTORY\tMESSAGE\tFIX")
		for _, f := range r.Findings {
			story := f.StoryID
			if story == "" {
				story = "-"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", f.Severity, f.Code, story, f.Message, f.Fix)
		}
		tw.Flush()
	}
	if r.Budget != nil {
		v := r.Budget.Verdict
		fmt.Fprintf(w, "\n── always-on context size ── %d B (~%d tok)", r.Budget.AlwaysOnBytes, r.Budget.AlwaysOnTokens)
		switch {
		case !v.HasBaseline:
			fmt.Fprintf(w, " — no baseline (run `satellites context budget <story> --set-baseline`)\n")
		case v.Grew:
			fmt.Fprintf(w, " — RATCHET: grew +%d B past baseline %d B\n", v.DeltaBytes, v.BaselineBytes)
		case v.DeltaBytes < 0:
			fmt.Fprintf(w, " — ok: shrank %d B below baseline %d B\n", -v.DeltaBytes, v.BaselineBytes)
		default:
			fmt.Fprintf(w, " — ok: at baseline %d B\n", v.BaselineBytes)
		}
	}
}

// plural is the "1 story" / "N stories" suffix helper.
func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
