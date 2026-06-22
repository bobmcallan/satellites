// `satellites task|story validate <id>` (sty_0454b437) — run the kind's ENTRY
// reviewer rubric READ-ONLY against an already-created artifact and print the
// {decision, notes} verdict, changing NOTHING (no status, no ledger). The
// reviewer is the same one the entry gate runs (task → satellites-task-upsert-
// review; story → satellites-intent-plan-review), but Bash is withheld so it
// cannot enact (ClaudeCLIGateDispatcher.ReadOnly). Exit is non-zero on reject so
// `validate` can gate a pre-flight / CI check without opening the artifact.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/spf13/cobra"
)

func newTaskValidateCmd(configArg, userArg *string) *cobra.Command {
	var claudeBin, worktreeRoot string
	cmd := &cobra.Command{
		Use:   "validate <task-id>",
		Short: "Run the task entry reviewer (satellites-task-upsert-review) READ-ONLY — judge structure, change nothing",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return runArtifactValidate(ctx, "task", strings.TrimSpace(args[0]), reviewOpts{
				ConfigPath: *configArg, UserArg: *userArg, ClaudeBin: claudeBin,
				WorktreeRoot: worktreeRoot, Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(),
			})
		},
	}
	cmd.Flags().StringVar(&claudeBin, "claude-bin", "", "Path to the claude binary (defaults to $SATELLITES_CLAUDE_BIN or `claude` on PATH).")
	cmd.Flags().StringVar(&worktreeRoot, "worktree", "", "Worktree root the reviewer runs against (default: current directory).")
	return cmd
}

func newStoryValidateCmd(configArg, userArg *string) *cobra.Command {
	var claudeBin, worktreeRoot string
	cmd := &cobra.Command{
		Use:   "validate <story-id>",
		Short: "Run the story entry reviewer (satellites-intent-plan-review) READ-ONLY — judge the plan, change nothing",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return runArtifactValidate(ctx, "story", strings.TrimSpace(args[0]), reviewOpts{
				ConfigPath: *configArg, UserArg: *userArg, ClaudeBin: claudeBin,
				WorktreeRoot: worktreeRoot, Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(),
			})
		},
	}
	cmd.Flags().StringVar(&claudeBin, "claude-bin", "", "Path to the claude binary (defaults to $SATELLITES_CLAUDE_BIN or `claude` on PATH).")
	cmd.Flags().StringVar(&worktreeRoot, "worktree", "", "Worktree root the reviewer runs against (default: current directory).")
	return cmd
}

// validateGateForKind is the per-kind FALLBACK entry reviewer, used only when no
// governing workflow resolves a forward entry edge (an unresolved/legacy
// artifact). The authoritative selection is entryReviewerForArtifact, which reads
// the gate from the workflow — "which gate runs" is configuration, not a Go switch
// (sty_8e9d409b).
func validateGateForKind(kind string) string {
	if kind == "story" {
		return "satellites-intent-plan-review"
	}
	return "satellites-task-upsert-review"
}

// entryReviewerForArtifact resolves the entry reviewer from the artifact's
// GOVERNING workflow (selector → category default → embedded copy) — the
// reviewer_skill on its forward entry edge — so the gate `validate` runs is the
// one the workflow declares, not a hardcoded kind→name map (sty_8e9d409b). Falls
// back to the per-kind default only when no governing workflow resolves a forward
// entry edge, preserving today's behaviour for an ungoverned artifact.
func entryReviewerForArtifact(tags []string, category, body, kind, configPath string) string {
	sources := governingWorkflowSources(configPath)
	if r := verb.EntryReviewer(verb.WorkflowSelector(tags), body, category, sources); r != "" {
		return r
	}
	return validateGateForKind(kind)
}

func runArtifactValidate(ctx context.Context, kind, id string, o reviewOpts) error {
	getReq, _ := json.Marshal(verb.DocumentGetRequest{ID: id})
	raw, err := dispatchVerb(ctx, "document_get", getReq, o.ConfigPath, o.UserArg)
	if err != nil {
		return fmt.Errorf("validate: resolve %s: %w", id, err)
	}
	var got verb.DocumentGetResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		return fmt.Errorf("validate: decode %s: %w", id, err)
	}
	body := got.RawBody
	if body == "" && len(got.Versions) > 0 {
		body = got.Versions[len(got.Versions)-1].Body
	}
	gate := entryReviewerForArtifact(got.Document.Tags, got.Document.Category, body, kind, o.ConfigPath)

	disp := verb.ClaudeCLIGateDispatcher{
		BinaryPath:     strings.TrimSpace(o.ClaudeBin),
		Model:          reviewerModel(o.ConfigPath),
		DefaultTimeout: gateDispatchTimeout,
		Fetch:          serverGateFetcher(o.ConfigPath, o.UserArg),
		ReadOnly:       true, // judge only — Bash withheld so the gate cannot enact
	}
	out, derr := disp.Dispatch(ctx, verb.GateInput{
		SkillName:    gate,
		StoryID:      got.Document.ID,
		ProjectID:    got.Document.ProjectID,
		WorkspaceID:  got.Document.WorkspaceID,
		StoryBody:    body,
		StoryStatus:  got.Document.Status,
		WorktreeRoot: o.WorktreeRoot,
	})
	if derr != nil {
		return fmt.Errorf("validate: gate dispatch: %w", derr)
	}

	w := o.Stdout
	if w == nil {
		w = io.Discard
	}
	fmt.Fprintf(w, "decision: %s\n", out.Decision)
	if strings.TrimSpace(out.Notes) != "" {
		fmt.Fprintf(w, "notes: %s\n", out.Notes)
	}
	if out.Decision != verb.GateDecisionAccept {
		return fmt.Errorf("validate: %s rejected by %s (read-only; nothing changed)", id, gate)
	}
	return nil
}
