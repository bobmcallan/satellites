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
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workflow"
	"github.com/spf13/cobra"
)

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
		asJSON   bool
		noLedger bool
	)
	cmd := &cobra.Command{
		Use:   "review <story-id>",
		Short: "Review a story's assembled context for structural conflicts (workflow × skills)",
		Long: `review checks the context the substrate would deliver for a story against the
structural invariants nothing else verifies: a degenerate workflow lifecycle
(no terminal / no initial / initial==terminal — reusing the workflow-review drift
gate), a transition naming a reviewer_skill that is not materialised, and an
unparseable ## Workflow.

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
			return runContextReview(ctx, strings.TrimSpace(args[0]), *configArg, *userArg, asJSON, noLedger, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Emit findings as JSON instead of a table.")
	cmd.Flags().BoolVar(&noLedger, "no-ledger", false, "Do not write context_conflict ledger rows; print only (dry inspection).")
	return cmd
}

func runContextReview(ctx context.Context, storyID, configPath, userArg string, asJSON, noLedger bool, stdout, stderr io.Writer) error {
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

	if !noLedger {
		for _, f := range findings {
			appendContextConflict(ctx, configPath, userArg, story, f, stderr)
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
// row the working agent can pull. Best-effort: a write failure warns but never
// fails the review (the operator still saw the finding on stdout).
func appendContextConflict(ctx context.Context, configPath, userArg string, story reviewStory, f conflictFinding, stderr io.Writer) {
	payload, _ := json.Marshal(map[string]any{
		"severity": f.Severity,
		"code":     f.Code,
		"message":  f.Message,
		"source":   "context-review",
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
