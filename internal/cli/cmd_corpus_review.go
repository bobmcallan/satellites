// `satellites skill review` and `satellites principle review` — a corpus-review
// sweep over the repo-local substrate source tree (sty_a6334862). It applies the
// authoring-as-config review (Spec/Verifier/Environment for skills; single-belief
// for principles) to what already exists — one artifact or `--all` — and prints a
// per-artifact verdict + a totals line.
//
// Read-only by default in spirit: `--dryrun` reports only. Without `--dryrun`,
// every REVISE artifact is revised IN PLACE in its `.satellites/<kind>/<name>.md`
// source via the matching authoring skill; the change is a plain git diff the
// operator inspects before commit + `upload` (those stay the operator's step).
//
// Both passes run the relevant substrate skill through `claude -p` from a NEUTRAL
// cwd with NO tools — the same isolation the caveman headline generator uses
// (cmd_document_headline.go, sty_f68f9053) — so the repo's CLAUDE.md / session
// context never leaks into the verdict and the subprocess cannot touch the tree.
// The Go engine owns every file write; claude only judges (review) or emits the
// revised markdown on stdout (revise).
//
// Fail-soft: a missing or erroring `claude`, or an unparseable verdict, leaves
// the artifact unchanged and is reported — the sweep never aborts on one
// artifact. No new MCP verbs: the review/authoring skill bodies come from the
// existing document_get surface.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/frontmatter"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/spf13/cobra"
)

// corpusReviewTimeout bounds one `claude -p` review or revise pass. A review
// reads one artifact and emits prose; a revise rewrites it — neither builds or
// tests, so this is far shorter than the reviewer-gate cap.
const corpusReviewTimeout = 5 * time.Minute

// corpusReviewOpts is the resolved invocation for one `review` run.
type corpusReviewOpts struct {
	Kind      string // "skills" | "principles" — the kind dir under .satellites/
	Name      string // single-target name (mutually exclusive with All)
	All       bool   // review every artifact under the kind dir
	DryRun    bool   // report only, no writes
	ClaudeBin string // override for the claude binary
	ConfigArg string
	UserArg   string
	Out       io.Writer
}

// reviewResult is one artifact's outcome — the row the summary prints.
type reviewResult struct {
	Name     string // artifact name (frontmatter override or filename stem)
	Path     string // source path under .satellites/ (printable)
	Verdict  string // "SHIP" | "REVISE" | "" (unknown, treated as Err)
	Findings string // the review skill's prose output
	Revised  bool   // the source file was rewritten this run (apply mode)
	Err      string // fail-soft error — pass failed; artifact left unchanged
}

// newCorpusReviewCmd builds the `review` subcommand bound to a kind dir. Used by
// both `skill review` and `principle review` so the two commands are identical
// except for the kind directory they sweep.
func newCorpusReviewCmd(kind string, configArg, userArg *string) *cobra.Command {
	var (
		all       bool
		dryRun    bool
		claudeBin string
	)
	cmd := &cobra.Command{
		Use:   "review [<name>]",
		Short: fmt.Sprintf("Review .satellites/%s source artifacts (SHIP/REVISE) and optionally revise in place", kind),
		Long: fmt.Sprintf(`review runs the matching review skill over %s source artifacts.

Target exactly one of <name> or --all. Each target's source file is fed to the
review skill via `+"`claude -p`"+` from a neutral cwd with no tools; the SHIP/REVISE
verdict + findings are printed. --dryrun reports only. Without --dryrun, every
REVISE artifact is revised in place in its .satellites/%s/ source via the
authoring skill — commit + upload remain your step.`, kind, kind),
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			name := ""
			if len(args) == 1 {
				name = strings.TrimSpace(args[0])
			}
			return runCorpusReview(ctx, corpusReviewOpts{
				Kind:      kind,
				Name:      name,
				All:       all,
				DryRun:    dryRun,
				ClaudeBin: claudeBin,
				ConfigArg: *configArg,
				UserArg:   *userArg,
				Out:       cmd.OutOrStdout(),
			})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, fmt.Sprintf("Review every artifact under .satellites/%s/ (mutually exclusive with <name>)", kind))
	cmd.Flags().BoolVar(&dryRun, "dryrun", false, "Report the verdicts only — make no writes")
	cmd.Flags().StringVar(&claudeBin, "claude-bin", "", "Path to the claude binary (defaults to $SATELLITES_CLAUDE_BIN or `claude` on PATH).")
	return cmd
}

// runCorpusReview resolves the target set and drives the engine. The review and
// revise passes are wired to the prod `claude -p` runners; tests construct the
// engine directly with stub passes.
func runCorpusReview(ctx context.Context, opts corpusReviewOpts) error {
	projectID, err := projectIDFromConfig(opts.ConfigArg)
	if err != nil {
		return err
	}
	allTargets, err := planUpload(resolveSubstrateRoot(opts.Kind, opts.ConfigArg), opts.Kind, projectID)
	if err != nil {
		return err
	}
	targets, err := selectReviewTargets(allTargets, opts.Name, opts.All)
	if err != nil {
		return err
	}

	reviewSkill := reviewSkillForKind(opts.Kind)       // satellites-skill-review | satellites-principle-review
	authoringSkill := authoringSkillForKind(opts.Kind) // satellites-skill-authoring | …-principle-authoring

	eng := corpusReviewEngine{
		kind:   opts.Kind,
		dryRun: opts.DryRun,
		reviewFn: func(ctx context.Context, raw string) (string, string, error) {
			body, err := fetchSystemSkillBody(ctx, reviewSkill, opts.ConfigArg, opts.UserArg)
			if err != nil {
				return "", "", fmt.Errorf("load review skill %q: %w", reviewSkill, err)
			}
			out, err := runClaudeNeutral(ctx, opts.ClaudeBin, body, corpusReviewPrompt(opts.Kind, raw))
			if err != nil {
				return "", "", fmt.Errorf("review pass: %w", err)
			}
			verdict, findings := parseReviewVerdict(out)
			if verdict == "" {
				return "", findings, fmt.Errorf("review pass: no SHIP/REVISE verdict in output")
			}
			return verdict, findings, nil
		},
		reviseFn: func(ctx context.Context, raw, findings string) (string, error) {
			body, err := fetchSystemSkillBody(ctx, authoringSkill, opts.ConfigArg, opts.UserArg)
			if err != nil {
				return "", fmt.Errorf("load authoring skill %q: %w", authoringSkill, err)
			}
			out, err := runClaudeNeutral(ctx, opts.ClaudeBin, body, corpusRevisePrompt(opts.Kind, raw, findings))
			if err != nil {
				return "", fmt.Errorf("revise pass: %w", err)
			}
			return sanitizeRevisedArtifact(out), nil
		},
	}
	return eng.run(ctx, opts.Out, targets)
}

// selectReviewTargets enforces the name-XOR-all contract and resolves the target
// set. Pure (no IO) so the selection rule is unit-tested directly.
//   - both set → error; neither set → error (exactly one is required).
//   - --all → every target (an empty tree yields none; the engine reports it).
//   - <name> → the single matching target, or an error if no source carries it.
func selectReviewTargets(targets []documentTarget, name string, all bool) ([]documentTarget, error) {
	switch {
	case all && name != "":
		return nil, fmt.Errorf("pass exactly one of <name> or --all, not both")
	case !all && name == "":
		return nil, fmt.Errorf("pass exactly one of <name> or --all")
	case all:
		return targets, nil
	default:
		for _, t := range targets {
			if t.Name == name {
				return []documentTarget{t}, nil
			}
		}
		return nil, fmt.Errorf("no source artifact named %q in the authoring source folder", name)
	}
}

// corpusReviewEngine owns the per-artifact loop. The two passes are function
// fields so production wires `claude -p` runners and tests inject stubs — the
// dryrun-no-writes invariant and the summary shape are then testable without a
// claude install.
type corpusReviewEngine struct {
	kind     string
	dryRun   bool
	reviewFn func(ctx context.Context, raw string) (verdict, findings string, err error)
	reviseFn func(ctx context.Context, raw, findings string) (revised string, err error)
}

// run reviews each target, applies revisions in apply mode, and prints the
// summary. It never returns an error for a per-artifact pass failure — that is
// recorded on the result and the sweep continues (fail-soft).
func (e corpusReviewEngine) run(ctx context.Context, out io.Writer, targets []documentTarget) error {
	if len(targets) == 0 {
		fmt.Fprintf(out, "no %s found under %s/ — nothing to review\n", e.kind, e.kind)
		return nil
	}
	results := make([]reviewResult, 0, len(targets))
	for _, t := range targets {
		results = append(results, e.reviewOne(ctx, t))
	}
	fmt.Fprint(out, formatReviewSummary(e.kind, e.dryRun, results))
	return nil
}

// reviewOne runs the review (and, in apply mode, the revise) pass for a single
// artifact. The source file is read from disk raw (frontmatter intact) so the
// review sees the whole artifact — a skill's description and a principle's
// residency tag both live in frontmatter.
func (e corpusReviewEngine) reviewOne(ctx context.Context, t documentTarget) reviewResult {
	r := reviewResult{Name: t.Name, Path: t.Path}
	raw, err := os.ReadFile(t.Path)
	if err != nil {
		r.Err = fmt.Sprintf("read source: %v", err)
		return r
	}
	verdict, findings, err := e.reviewFn(ctx, string(raw))
	if err != nil {
		r.Err = err.Error()
		r.Findings = findings
		return r
	}
	r.Verdict, r.Findings = verdict, findings

	// Apply mode: a REVISE artifact is revised in place. A pass error or an
	// empty/unchanged result leaves the file untouched and is reported.
	if !e.dryRun && verdict == "REVISE" {
		revised, rerr := e.reviseFn(ctx, string(raw), findings)
		switch {
		case rerr != nil:
			r.Err = rerr.Error()
		case strings.TrimSpace(revised) == "":
			r.Err = "revise pass produced empty output — left unchanged"
		case revised == string(raw):
			// No-op rewrite — nothing to record.
		default:
			if werr := os.WriteFile(t.Path, []byte(revised), 0o644); werr != nil {
				r.Err = fmt.Sprintf("write revised source: %v", werr)
			} else {
				r.Revised = true
			}
		}
	}
	return r
}

// formatReviewSummary renders the per-artifact rows + a totals line. Pure (takes
// the results, returns the text) so the summary shape is unit-tested. In dryrun
// mode the header is tagged and no "revised" annotations appear.
func formatReviewSummary(kind string, dryRun bool, results []reviewResult) string {
	var b strings.Builder
	mode := ""
	if dryRun {
		mode = " [dry-run]"
	}
	fmt.Fprintf(&b, "reviewed %d %s artifact(s)%s\n\n", len(results), kind, mode)

	var ship, revise, errs, revised int
	for _, r := range results {
		switch {
		case r.Err != "":
			errs++
			fmt.Fprintf(&b, "%s: error — %s\n", r.Name, r.Err)
		case r.Verdict == "REVISE":
			revise++
			if !dryRun && r.Revised {
				revised++
				fmt.Fprintf(&b, "%s: REVISE → revised %s\n", r.Name, r.Path)
			} else {
				fmt.Fprintf(&b, "%s: REVISE\n", r.Name)
			}
		case r.Verdict == "SHIP":
			ship++
			fmt.Fprintf(&b, "%s: SHIP\n", r.Name)
		default:
			errs++
			fmt.Fprintf(&b, "%s: error — no verdict\n", r.Name)
		}
		if f := strings.TrimSpace(r.Findings); f != "" {
			for _, ln := range strings.Split(f, "\n") {
				fmt.Fprintf(&b, "    %s\n", ln)
			}
		}
	}

	fmt.Fprintf(&b, "\ntotals: %d reviewed · %d SHIP · %d REVISE · %d error(s)", len(results), ship, revise, errs)
	if !dryRun {
		fmt.Fprintf(&b, " · %d revised", revised)
	}
	b.WriteByte('\n')
	return b.String()
}

// reviewSkillForKind lives in content_review.go (skills→satellites-skill-review,
// principles→satellites-principle-review). authoringSkillForKind is its authoring twin:
// the system skill that drives the revise pass.
func authoringSkillForKind(kind string) string {
	return "satellites-" + strings.TrimSuffix(kind, "s") + "-authoring"
}

// kindNoun renders the singular noun for a kind dir, for prompt wording.
func kindNoun(kind string) string {
	return strings.TrimSuffix(kind, "s")
}

// corpusReviewPrompt is the stdin task for the review pass: an explicit one-shot
// instruction (so no ambient context is needed) followed by the artifact.
func corpusReviewPrompt(kind, raw string) string {
	return fmt.Sprintf(
		"Review the %s artifact below per your instructions. Report your findings and end with exactly one verdict line: SHIP or REVISE.\n\n%s",
		kindNoun(kind), raw)
}

// corpusRevisePrompt is the stdin task for the revise pass: rewrite the artifact
// to address the findings, emitting ONLY the complete revised markdown file. The
// Go engine writes that output back to the source — claude has no tools and
// never touches the tree itself.
func corpusRevisePrompt(kind, raw, findings string) string {
	return fmt.Sprintf(
		"Revise the %s artifact below to address the review findings. Output ONLY the complete revised markdown file, including its frontmatter — no commentary, no code fence.\n\n=== Review findings ===\n%s\n\n=== Current artifact ===\n%s",
		kindNoun(kind), strings.TrimSpace(findings), raw)
}

// parseReviewVerdict extracts the SHIP/REVISE verdict from the review skill's
// prose. The skill ends with "one verdict: SHIP / REVISE", so the verdict is the
// last line that names exactly one of them — a line naming both (e.g. echoing the
// instruction "SHIP / REVISE") is ambiguous and skipped. The full trimmed output
// is the findings. An empty verdict signals an unparseable result (fail-soft).
func parseReviewVerdict(out string) (verdict, findings string) {
	findings = strings.TrimSpace(out)
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		u := strings.ToUpper(lines[i])
		hasShip := strings.Contains(u, "SHIP")
		hasRevise := strings.Contains(u, "REVISE")
		if hasShip && hasRevise {
			continue
		}
		if hasRevise {
			return "REVISE", findings
		}
		if hasShip {
			return "SHIP", findings
		}
	}
	return "", findings
}

// sanitizeRevisedArtifact strips a wrapping markdown code fence the model may add
// despite the "no code fence" instruction, then trims trailing whitespace and
// ensures a single trailing newline — matching the authored source-file shape.
func sanitizeRevisedArtifact(out string) string {
	s := strings.TrimSpace(out)
	if strings.HasPrefix(s, "```") {
		if nl := strings.IndexByte(s, '\n'); nl >= 0 {
			s = s[nl+1:]
		}
		if end := strings.LastIndex(s, "```"); end >= 0 {
			s = s[:end]
		}
		s = strings.TrimSpace(s)
	}
	if s == "" {
		return ""
	}
	return s + "\n"
}

// fetchSystemSkillBody loads a system-scope skill's body via document_get and
// returns it frontmatter-stripped — the rubric the `claude -p` pass runs under as
// its appended system prompt. Fetched from the substrate (not disk) so it works
// whether or not the skill is materialised under .claude/skills/ locally.
func fetchSystemSkillBody(ctx context.Context, name, configArg, userArg string) (string, error) {
	req, err := json.Marshal(verb.DocumentGetRequest{Scope: "system", Name: name})
	if err != nil {
		return "", err
	}
	raw, err := dispatchVerb(ctx, "document_get", req, configArg, userArg)
	if err != nil {
		return "", err
	}
	var resp verb.DocumentGetResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", err
	}
	body := resp.RawBody
	if body == "" && len(resp.Versions) > 0 {
		body = resp.Versions[0].Body
	}
	if strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("skill %q has no body", name)
	}
	if _, stripped, perr := frontmatter.Parse([]byte(body)); perr == nil {
		return string(stripped), nil
	}
	return body, nil
}

// runClaudeNeutral runs one `claude -p` pass from a NEUTRAL cwd with NO tools,
// mirroring the headline generator (cmd_document_headline.go): a temp cwd so the
// repo's CLAUDE.md/session context cannot leak in, and an empty --allowedTools so
// the subprocess reads only its stdin and never touches the tree. The skill body
// is the appended system prompt; the task arrives on stdin. Returns the raw
// completion; any subprocess failure is an error the caller treats as fail-soft.
func runClaudeNeutral(ctx context.Context, claudeBin, systemPrompt, stdin string) (string, error) {
	bin := strings.TrimSpace(claudeBin)
	if bin == "" {
		bin = strings.TrimSpace(os.Getenv("SATELLITES_CLAUDE_BIN"))
	}
	if bin == "" {
		bin = "claude"
	}
	cctx, cancel := context.WithTimeout(ctx, corpusReviewTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, "-p", "--allowedTools", "", "--append-system-prompt", systemPrompt)
	cmd.Dir = os.TempDir()
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = os.Environ()
	outBytes, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(outBytes), nil
}
