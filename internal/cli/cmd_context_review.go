// `satellites context review <story>` — review the assembled delivered context
// for STRUCTURAL conflicts and surface them to BOTH the operator and the working
// agent (epic:qa-observability, story:sty_414d1a2a). The review layer over the
// bundle order:2 (`context show`) renders: enforcement faithfully executes
// whatever context got assembled, conflicts and all — this catches the
// structural ones before they drive work.
//
// First cut = the workflow × skills layer, deterministic:
//   - workflow-lifecycle — reuse the workflow-review drift gate's validator
//     (story:sty_1604064f = workflow.ValidateLifecycle): no terminal, no initial,
//     or initial==terminal.
//   - missing-gate-skill — a transition names a reviewer_skill that is not
//     materialised in .claude/skills.
//   - workflow-unparseable — the story has no parseable ## Workflow block.
//
// Surfacing (AC2): the operator reads findings on stdout (or --json); the working
// agent pulls them from the ledger (kind:"context_conflict") via
// `ledger_list {story_id, kind:"context_conflict"}` — a flagged note, not silent.
// The semantic layer (contradictory principles, principle × skill — an LLM pass)
// is the follow-up story:sty_8465e964. No new MCP verb: a client render over the
// existing read verbs + internal/workflow + ledger_append.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/bobmcallan/satellites/internal/frontmatter"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workflow"
	"github.com/spf13/cobra"
)

// semanticReviewSkill is the claude -p reviewer that judges the assembled bundle
// for the conflicts a parser cannot (sty_8465e964). semanticReviewTimeout bounds
// the one LLM call.
const semanticReviewSkill = "satellites-context-conflict-review"
const semanticReviewTimeout = 5 * time.Minute

// conflictFinding is one structural conflict in the assembled context. Mirrors
// the reviewer.Finding shape (severity/code/message) so the two finding streams
// (this and the semantic follow-up) compose.
type conflictFinding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// contextConflictLedgerKind is the ledger row kind the working agent pulls to
// see context-review findings (`ledger_list {story_id, kind:"context_conflict"}`).
const contextConflictLedgerKind = "context_conflict"

func newContextReviewCmd(configArg, userArg *string) *cobra.Command {
	var (
		asJSON    bool
		noLedger  bool
		semantic  bool
		claudeBin string
	)
	cmd := &cobra.Command{
		Use:   "review <story-id>",
		Short: "Review a story's assembled context for conflicts (structural; --semantic adds the LLM layer)",
		Long: `review checks the context the substrate would deliver for a story against the
structural invariants nothing else verifies: a degenerate workflow lifecycle
(no terminal / no initial / initial==terminal — reusing the workflow-review drift
gate), a transition naming a reviewer_skill that is not materialised, and an
unparseable ## Workflow.

--semantic additionally runs an isolated claude -p reviewer
(satellites-context-conflict-review) over the assembled bundle (project
principles + skills index + the story's ## Workflow) and folds its judgement-
requiring findings (contradictory principles, principle × skill conflicts, a
required step that is not gated) into the same stream.

Findings surface to BOTH audiences: the operator (stdout, or --json) and the
working agent (a ledger row kind:"context_conflict", pulled via
ledger_list {story_id, kind:"context_conflict"}). Out of band — never inside the
executor's turn. --no-ledger prints without writing the agent-facing rows.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return runContextReview(ctx, strings.TrimSpace(args[0]), *configArg, *userArg, asJSON, noLedger, semantic, claudeBin, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Emit findings as JSON instead of a table.")
	cmd.Flags().BoolVar(&noLedger, "no-ledger", false, "Do not write context_conflict ledger rows; print only (dry inspection).")
	cmd.Flags().BoolVar(&semantic, "semantic", false, "Also run the claude -p semantic reviewer over the assembled bundle.")
	cmd.Flags().StringVar(&claudeBin, "claude-bin", "", "Path to the claude binary for --semantic (defaults to $SATELLITES_CLAUDE_BIN or `claude`).")
	return cmd
}

func runContextReview(ctx context.Context, storyID, configPath, userArg string, asJSON, noLedger, semantic bool, claudeBin string, stdout, stderr io.Writer) error {
	if storyID == "" {
		return fmt.Errorf("story id required")
	}
	story, body, err := reviewGetStory(ctx, reviewOpts{StoryID: storyID, ConfigPath: configPath, UserArg: userArg})
	if err != nil {
		return err
	}
	skillExists := func(name string) bool {
		_, statErr := os.Stat(filepath.Join(".claude", "skills", name, "SKILL.md"))
		return statErr == nil
	}
	findings := reviewContextConflicts(body, skillExists)
	sources := make([]string, len(findings))
	for i := range sources {
		sources[i] = "context-review"
	}

	// --semantic: fold the LLM reviewer's judgement-requiring findings into the
	// SAME stream (AC2). A dispatch/parse failure warns but never fails the
	// review — the structural findings still stand.
	if semantic {
		sem, sErr := semanticConflicts(ctx, body, claudeBin, stderr)
		if sErr != nil {
			fmt.Fprintf(stderr, "warn: semantic context review: %v\n", sErr)
		}
		for _, f := range sem {
			findings = append(findings, f)
			sources = append(sources, "context-review-semantic")
		}
	}

	if !noLedger {
		for i, f := range findings {
			appendContextConflict(ctx, configPath, userArg, story, f, sources[i], stderr)
		}
	}

	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"story_id": storyID, "findings": findings})
	}
	renderConflicts(stdout, storyID, findings, noLedger)
	return nil
}

// reviewContextConflicts is the pure structural check — parse the story's
// ## Workflow, validate its lifecycle (reusing sty_1604064f), and cross-check
// each transition's reviewer_skill against skillExists. Pure over its inputs so
// it is unit-testable without dispatch or the filesystem.
func reviewContextConflicts(storyBody string, skillExists func(string) bool) []conflictFinding {
	wf, err := workflow.ParseBody([]byte(storyBody))
	if err != nil {
		return []conflictFinding{{
			Severity: "error",
			Code:     "workflow-unparseable",
			Message:  fmt.Sprintf("no parseable ## Workflow block: %v", err),
		}}
	}
	var out []conflictFinding
	if lerr := wf.ValidateLifecycle(); lerr != nil {
		out = append(out, conflictFinding{Severity: "error", Code: "workflow-lifecycle", Message: lerr.Error()})
	}
	seen := map[string]bool{}
	for _, t := range wf.Transitions {
		skill := strings.TrimSpace(t.ReviewerSkill)
		if skill == "" || seen[skill] {
			continue // an unguarded edge is allowed; report each missing skill once
		}
		if !skillExists(skill) {
			seen[skill] = true
			out = append(out, conflictFinding{
				Severity: "error",
				Code:     "missing-gate-skill",
				Message:  fmt.Sprintf("transition %s→%s names reviewer_skill %q but no such skill is materialised in .claude/skills", t.From, t.To, skill),
			})
		}
	}
	return out
}

// appendContextConflict writes one finding to the ledger as a context_conflict
// row the working agent can pull. source distinguishes the structural layer
// ("context-review") from the semantic LLM layer ("context-review-semantic") so
// both compose into one stream. Best-effort: a write failure warns but never
// fails the review (the operator still saw the finding on stdout).
func appendContextConflict(ctx context.Context, configPath, userArg string, story reviewStory, f conflictFinding, source string, stderr io.Writer) {
	payload, _ := json.Marshal(map[string]any{
		"severity": f.Severity,
		"code":     f.Code,
		"message":  f.Message,
		"source":   source,
	})
	req, err := json.Marshal(verb.LedgerAppendRequest{
		StoryID:     story.ID,
		ProjectID:   story.ProjectID,
		WorkspaceID: story.WorkspaceID,
		Kind:        contextConflictLedgerKind,
		Body:        f.Message,
		Payload:     payload,
	})
	if err != nil {
		return
	}
	if _, err := dispatchVerb(ctx, "ledger_append", req, configPath, userArg); err != nil {
		fmt.Fprintf(stderr, "warn: write context_conflict to ledger: %v\n", err)
	}
}

// renderConflicts prints the findings table (or a clean line). noLedger is noted
// so the operator knows whether the agent-facing rows were written.
func renderConflicts(w io.Writer, storyID string, findings []conflictFinding, noLedger bool) {
	if len(findings) == 0 {
		fmt.Fprintf(w, "context review %s: no conflicts\n", storyID)
		return
	}
	fmt.Fprintf(w, "context review %s: %d conflict(s)\n\n", storyID, len(findings))
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "SEVERITY\tCODE\tMESSAGE")
	for _, f := range findings {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", f.Severity, f.Code, f.Message)
	}
	tw.Flush()
	if noLedger {
		fmt.Fprintf(w, "\n(--no-ledger: context_conflict rows not written; agent will not see these)\n")
	} else {
		fmt.Fprintf(w, "\nwritten as context_conflict ledger rows — agent: ledger_list {story_id:%q, kind:%q}\n", storyID, contextConflictLedgerKind)
	}
}

// semanticConflicts runs the claude -p semantic reviewer over the assembled
// bundle (project principles + skills index + the story's ## Workflow) and parses
// its findings. The judgement layer order:3 left to this story (AC1/AC4).
func semanticConflicts(ctx context.Context, storyBody, claudeBin string, stderr io.Writer) ([]conflictFinding, error) {
	skillBody, err := skillBodyOf(semanticReviewSkill)
	if err != nil {
		return nil, fmt.Errorf("read %s skill (run `satellites skill upload && satellites skill sync`?): %w", semanticReviewSkill, err)
	}
	bundle := buildSemanticBundle(storyBody)
	raw, err := dispatchClaudeSemantic(ctx, claudeBin, skillBody, bundle)
	if err != nil {
		return nil, err
	}
	return parseSemanticFindings(raw)
}

// buildSemanticBundle marshals the inputs the semantic reviewer judges: the
// project principles (sources), the skills index (name/kind/description), and the
// story's ## Workflow. Pure over its inputs + the repo's .satellites/.claude
// trees (AC1: the real principles + skills index + workflow).
func buildSemanticBundle(storyBody string) string {
	type principle struct {
		Name string `json:"name"`
		Body string `json:"body"`
	}
	var principles []principle
	dir := filepath.Join(".satellites", "principles")
	if entries, derr := os.ReadDir(dir); derr == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			raw, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
			if rerr != nil {
				continue
			}
			fm, b, perr := frontmatter.Parse(raw)
			name := strings.TrimSpace(fm.Name)
			if perr != nil || name == "" {
				name = strings.TrimSuffix(e.Name(), ".md")
			}
			principles = append(principles, principle{Name: name, Body: strings.TrimSpace(string(b))})
		}
	}

	type skill struct {
		Name        string `json:"name"`
		Kind        string `json:"kind"`
		Description string `json:"description"`
	}
	var skills []skill
	for _, s := range materialisedSkills() {
		skills = append(skills, skill{Name: s.name, Kind: s.kind, Description: s.description})
	}

	payload := map[string]any{
		"principles": principles,
		"skills":     skills,
		"workflow":   extractSection(storyBody, "## Workflow"),
	}
	b, _ := json.Marshal(payload)
	return string(b)
}

// dispatchClaudeSemantic runs the semantic reviewer: claude -p with the skill as
// the system prompt and the bundle on stdin.
func dispatchClaudeSemantic(ctx context.Context, claudeBin, skillBody, bundle string) (string, error) {
	bin := strings.TrimSpace(claudeBin)
	if bin == "" {
		bin = strings.TrimSpace(os.Getenv("SATELLITES_CLAUDE_BIN"))
	}
	if bin == "" {
		bin = "claude"
	}
	cctx, cancel := context.WithTimeout(ctx, semanticReviewTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, "-p", "--allowedTools", "Read Grep Glob", "--append-system-prompt", skillBody)
	cmd.Stdin = strings.NewReader(bundle)
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("claude -p: %w", err)
	}
	return string(out), nil
}

// parseSemanticFindings tolerantly extracts the {"findings":[…]} object from the
// reviewer's stdout (strips fences/prose, takes the outermost object). An empty
// or absent findings array yields no findings (a coherent bundle). Pure.
func parseSemanticFindings(raw string) ([]conflictFinding, error) {
	s := strings.TrimSpace(raw)
	lo := strings.IndexByte(s, '{')
	hi := strings.LastIndexByte(s, '}')
	if lo < 0 || hi <= lo {
		return nil, fmt.Errorf("no JSON object in semantic reviewer output (raw=%.200q)", raw)
	}
	var doc struct {
		Findings []conflictFinding `json:"findings"`
	}
	if err := json.Unmarshal([]byte(s[lo:hi+1]), &doc); err != nil {
		return nil, fmt.Errorf("parse semantic findings: %w", err)
	}
	// Default a missing severity so the row renders + writes coherently.
	for i := range doc.Findings {
		if strings.TrimSpace(doc.Findings[i].Severity) == "" {
			doc.Findings[i].Severity = "warn"
		}
	}
	return doc.Findings, nil
}
