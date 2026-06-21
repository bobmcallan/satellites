// `satellites validate` (alias `doctor`) — diagnose this project's governance
// (principles, skills, workflows) against the CURRENT binary's rules and report
// a per-artifact verdict: OK / STALE / UNRESOLVABLE. It exists so drift
// introduced by an out-of-session `satellites update` surfaces deliberately
// (e.g. a workflow naming a reviewer the new binary can no longer resolve —
// the kind:gate-vs-reviewer contradiction the VIRE session hit) instead of
// silently breaking the next gate.
//
// It is read-only and a pure COMPOSITION — no MCP verb, no new judging in Go:
//   - the STATIC pass (default) reuses the `workflow check` resolver verbatim
//     (`runWorkflowChecksResolved`), rolling its drift findings up per artifact;
//   - `--deep` additionally runs the existing `claude -p` content review over
//     skills + principles (report-only).
//
// Exit is non-zero when any artifact is UNRESOLVABLE, so the same code path
// gates CI and `satellites init` (epic:governance-lifecycle order-3).

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/spf13/cobra"
)

// artifactVerdict is one row of the validate report.
type artifactVerdict struct {
	Verdict  string `json:"verdict"` // OK | STALE | UNRESOLVABLE
	Kind     string `json:"kind"`    // principle | workflow | gate | reviewer | skill
	Artifact string `json:"artifact"`
	Reason   string `json:"reason,omitempty"`
}

// artifactRef names one governance artifact in the validate roster.
type artifactRef struct {
	name string
	kind string
}

func init() {
	var (
		configArg string
		userArg   string
		deep      bool
		asJSON    bool
	)
	cmd := &cobra.Command{
		Use:     "validate",
		Aliases: []string{"doctor"},
		Short:   "Diagnose this project's governance (principles/skills/workflows) against the current binary",
		Long: `validate (alias: doctor) evaluates the caller's project governance —
principles, skills, and workflows — against the CURRENT binary's rules and
reports a per-artifact verdict: OK / STALE / UNRESOLVABLE. Read-only.

It composes existing machinery, adding no MCP verb:
  - STATIC (default): the ` + "`workflow check`" + ` resolver — every workflow's
    reviewer/gate reference must resolve (embed → local → server). A workflow
    naming a reviewer the binary can't resolve is UNRESOLVABLE. Fast; cheap
    enough to run on every init.
  - --deep: also runs the claude -p content review over skills + principles
    (report-only).

Exit is non-zero when any artifact is UNRESOLVABLE, so validate gates CI and
init. After fixing drift, re-author/re-upload the artifact, or archive it with
` + "`satellites clear`" + `.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidate(cmd.Context(), cmd.OutOrStdout(), configArg, userArg, deep, asJSON)
		},
	}
	cmd.Flags().BoolVar(&deep, "deep", false, "Also run the claude -p content review over skills + principles (slower; read-only)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Emit the per-artifact verdicts as a JSON array")
	cmd.Flags().StringVar(&configArg, "config", "", "Path to satellites.toml (overrides $SATELLITES_CONFIG / .satellites/satellites.toml walk-up).")
	cmd.Flags().StringVar(&userArg, "user", "", "Caller user id (overrides $SATELLITES_USER_ID).")
	register(cmd)
}

// runValidate runs the static resolver pass (and optionally the deep content
// pass), renders the per-artifact verdicts, and returns a non-nil error (exit
// non-zero) when any artifact is UNRESOLVABLE.
func runValidate(ctx context.Context, out io.Writer, configArg, userArg string, deep, asJSON bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	verdicts, err := validateGovernance(ctx, configArg, userArg)
	if err != nil {
		return err
	}

	if !asJSON && deep {
		// Read-only content audit; the corpus reviewer prints its own SHIP/REVISE
		// findings. Best-effort — a content-review error never masks the static
		// verdict that gates init/CI.
		fmt.Fprintln(out, "\n── deep content review (skills) ──")
		_ = runCorpusReview(ctx, corpusReviewOpts{Kind: "skills", All: true, DryRun: true, ConfigArg: configArg, UserArg: userArg, Out: out})
		fmt.Fprintln(out, "\n── deep content review (principles) ──")
		_ = runCorpusReview(ctx, corpusReviewOpts{Kind: "principles", All: true, DryRun: true, ConfigArg: configArg, UserArg: userArg, Out: out})
	}

	return reportValidate(out, verdicts, asJSON)
}

// validateGovernance computes the per-artifact verdicts for the caller's
// project — the STATIC resolver pass, read-only. It is the single source of
// truth shared by the `validate` command and the `init` post-scaffold pass
// (epic:governance-lifecycle order-3); neither re-implements the resolution.
func validateGovernance(ctx context.Context, configArg, userArg string) ([]artifactVerdict, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	stories, err := listProjectStories(ctx, configArg, userArg)
	if err != nil {
		return nil, err
	}
	skills := markLocalAuthorship(materialisedSkills(), configArg)
	wfs := withEmbeddedWorkflows(clientWorkflows(configArg))
	fetch := serverGateFetcher(configArg, userArg)
	gateResolvable := func(g string) bool { return verb.GateResolvable(ctx, fetch, ".", g) }
	findings := runWorkflowChecksResolved(skills, wfs, stories, gateResolvable)
	// Surface a non-task-shaped workflow claiming category:task (sty_d3fead0d) —
	// it shadows the canonical satellites-task-workflow.
	findings = append(findings, checkTaskWorkflowShadows(wfs)...)
	roster := validateRoster(ctx, skills, wfs, configArg, userArg)
	return rollupVerdicts(roster, findings), nil
}

// validateRoster builds the complete governance roster — every workflow-bearing
// skill (incl. kind:workflow/gate/reviewer) plus the project's principles — so
// the report lists OK artifacts too, not only the ones with findings.
func validateRoster(ctx context.Context, skills, wfs []matSkill, configArg, userArg string) []artifactRef {
	seen := map[string]bool{}
	var roster []artifactRef
	add := func(name, kind string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		roster = append(roster, artifactRef{name: name, kind: kind})
	}
	for _, s := range append(append([]matSkill{}, skills...), wfs...) {
		kind := s.kind
		if kind == "" {
			kind = "skill"
		}
		add(s.name, kind)
	}
	for _, name := range projectPrincipleNames(ctx, configArg, userArg) {
		add(name, "principle")
	}
	return roster
}

// projectPrincipleNames lists the caller's project-scoped principle rows by
// reusing the project-scoped listing (best-effort — an unconfigured/unreachable
// caller simply contributes no principle rows to the roster).
func projectPrincipleNames(ctx context.Context, configArg, userArg string) []string {
	projectID, err := projectIDFromConfig(configArg)
	if err != nil || strings.TrimSpace(projectID) == "" {
		return nil
	}
	wsID, err := resolveWorkspaceID(ctx, projectID, configArg, userArg)
	if err != nil {
		return nil
	}
	dispatch := func(c context.Context, name string, req json.RawMessage) (json.RawMessage, error) {
		return dispatchVerb(c, name, req, configArg, userArg)
	}
	items, err := listProjectScoped(ctx, dispatch, "", projectID, wsID)
	if err != nil {
		return nil
	}
	var names []string
	for _, it := range filterByTagPrefix(items, "principles:") {
		names = append(names, it.Name)
	}
	return names
}

// rollupVerdicts maps the resolver's drift findings onto each roster artifact:
// any block finding → UNRESOLVABLE, else any advise finding → STALE, else OK.
// A finding whose artifact is not in the roster is still surfaced (defensive)
// so a drift class keyed to an unexpected name is never silently dropped.
func rollupVerdicts(roster []artifactRef, findings []driftFinding) []artifactVerdict {
	byArtifact := map[string][]driftFinding{}
	for _, f := range findings {
		byArtifact[f.Artifact] = append(byArtifact[f.Artifact], f)
	}
	inRoster := map[string]bool{}
	var out []artifactVerdict
	for _, r := range roster {
		inRoster[r.name] = true
		out = append(out, verdictFor(r.kind, r.name, byArtifact[r.name]))
	}
	for name, fs := range byArtifact {
		if !inRoster[name] {
			out = append(out, verdictFor("skill", name, fs))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Verdict != out[j].Verdict {
			return validateSeverityRank(out[i].Verdict) > validateSeverityRank(out[j].Verdict)
		}
		return out[i].Artifact < out[j].Artifact
	})
	return out
}

func verdictFor(kind, name string, fs []driftFinding) artifactVerdict {
	verdict, reason := "OK", ""
	for _, f := range fs {
		if f.Severity == "block" {
			return artifactVerdict{Verdict: "UNRESOLVABLE", Kind: kind, Artifact: name, Reason: f.Code + ": " + f.Message}
		}
		if verdict == "OK" {
			verdict, reason = "STALE", f.Code+": "+f.Message
		}
	}
	return artifactVerdict{Verdict: verdict, Kind: kind, Artifact: name, Reason: reason}
}

func validateSeverityRank(v string) int {
	switch v {
	case "UNRESOLVABLE":
		return 2
	case "STALE":
		return 1
	default:
		return 0
	}
}

// reportValidate prints the per-artifact verdicts and returns a non-nil error
// (exit non-zero) when any artifact is UNRESOLVABLE.
func reportValidate(out io.Writer, verdicts []artifactVerdict, asJSON bool) error {
	unresolvable := 0
	stale := 0
	for _, v := range verdicts {
		switch v.Verdict {
		case "UNRESOLVABLE":
			unresolvable++
		case "STALE":
			stale++
		}
	}
	if asJSON {
		b, _ := json.MarshalIndent(verdicts, "", "  ")
		fmt.Fprintln(out, string(b))
	} else {
		for _, v := range verdicts {
			line := fmt.Sprintf("%-13s %-10s %s", v.Verdict, v.Kind, v.Artifact)
			if v.Reason != "" {
				line += "  — " + v.Reason
			}
			fmt.Fprintln(out, line)
		}
		fmt.Fprintln(out, "\n── governance validate ──")
		switch {
		case unresolvable > 0:
			fmt.Fprintf(out, "verdict: UNRESOLVABLE — %d artifact(s) the current binary cannot resolve (%d stale). Re-author/re-upload or `satellites clear`.\n", unresolvable, stale)
		case stale > 0:
			fmt.Fprintf(out, "verdict: OK with %d advisory (stale) finding(s) — all governance resolves.\n", stale)
		default:
			fmt.Fprintf(out, "verdict: OK — all %d governance artifact(s) resolve against the current binary.\n", len(verdicts))
		}
	}
	if unresolvable > 0 {
		return fmt.Errorf("validate: %d unresolvable governance artifact(s)", unresolvable)
	}
	return nil
}
