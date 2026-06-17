// `satellites workflow embed <story-id>` — the config-driven insertion of a
// story's governing workflow (epic:client-dir-separation order-2). Resolves the
// workflow whose `applies_to` covers the story's category over the merged source
// set (client-dir .satellites/workflows first, then materialised kind:workflow
// skills), stamps its `## Workflow` yaml into the story body, and prints the
// resolved workflow + the next gate to request.
//
// This REPLACES the agent hand-copying yaml from a skill: resolution is
// `applies_to ↔ category` (config over code), the executor never picks a
// filename, and one call both PERSISTS the embedded copy (for the gates / server
// / editability hook that parse it) and SURFACES the process. Fail-closed: when
// no workflow covers the category the command errors and stamps nothing.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workflow"
	"github.com/spf13/cobra"
)

func newWorkflowEmbedCmd(configArg, userArg *string) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "embed <story-id>",
		Short: "Resolve the story's governing workflow by category and stamp its ## Workflow into the story",
		Long: `embed resolves the governing workflow for a story — the one whose applies_to
covers the story's category, over .satellites/workflows (client-dir, preferred)
then the materialised kind:workflow skills — and writes its ` + "`## Workflow`" + ` yaml
into the story body. Run it at story start instead of hand-copying the workflow
from a skill. It also prints the workflow and the next gate to request.

Fail-closed: when no workflow covers the story's category, embed errors and
changes nothing.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return runWorkflowEmbed(ctx, cmd.OutOrStdout(), *configArg, *userArg, strings.TrimSpace(args[0]), asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Emit the resolved workflow + next gate as JSON")
	return cmd
}

func runWorkflowEmbed(ctx context.Context, out io.Writer, configPath, userArg, storyID string, asJSON bool) error {
	if storyID == "" {
		return fmt.Errorf("story id required")
	}
	story, body, err := reviewGetStory(ctx, reviewOpts{StoryID: storyID, ConfigPath: configPath, UserArg: userArg})
	if err != nil {
		return err
	}

	// Resolve the governing workflow by category over the merged sources.
	sources := governingWorkflowSources(configPath)
	wf, name, ok := verb.ResolveGoverningWorkflow(story.Category, sources)
	if !ok {
		return fmt.Errorf("workflow embed: no workflow covers category %q — author one under .satellites/workflows (frontmatter applies_to) before embedding (fail closed)", story.Category)
	}

	// Stamp the embedded `## Workflow` from the governing definition. Shared with
	// the review path's self-heal (reembedGoverningWorkflow) so there is one
	// stamping implementation; idempotent (writes only when absent/drifted).
	_, changed, _, _, err := reembedGoverningWorkflow(ctx, story, body, configPath, userArg, sources)
	if err != nil {
		return fmt.Errorf("workflow embed: %w", err)
	}

	// srcBody for the printed table (state machine + checkpoint gates).
	var srcBody string
	for _, s := range sources {
		if s.Name == name {
			srcBody = s.Body
			break
		}
	}

	// Next move out of the story's current status. Prefer a forward move: an
	// ungated checkpoint trigger, else a gated edge into a non-terminal state
	// (so a cancellation edge — into a terminal state — is not surfaced as "the
	// next step"). Pure projection over the resolved workflow shape.
	nextGate, nextTo, checkpoint := "", "", false
	for _, t := range wf.TransitionsFrom(story.Status) {
		if t.Trigger == "checkpoint" && strings.TrimSpace(t.ReviewerSkill) == "" {
			nextTo, checkpoint = t.To, true
		}
	}
	if !checkpoint {
		for _, t := range wf.TransitionsFrom(story.Status) {
			if g := strings.TrimSpace(t.ReviewerSkill); g != "" && !wf.IsTerminal(t.To) {
				nextGate, nextTo = g, t.To
			}
		}
	}

	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"story_id": storyID, "category": story.Category, "workflow": name,
			"status": story.Status, "embedded_changed": changed,
			"next_gate": nextGate, "next_status": nextTo, "next_is_checkpoint": checkpoint,
		})
	}

	action := "embedded"
	if !changed {
		action = "already current"
	}
	fmt.Fprintf(out, "workflow %q governs %s (category %q) — ## Workflow %s.\n\n", name, storyID, story.Category, action)
	printWorkflowTable(out, wf, checkpointGates(srcBody))
	switch {
	case nextGate != "":
		fmt.Fprintf(out, "\nnext: satellites story status_transition %s --skill %s   (→ %s)\n", storyID, nextGate, nextTo)
	case checkpoint:
		fmt.Fprintf(out, "\nnext: at a checkpoint, request the traverse (→ %s) — see the checkpoint gates above.\n", nextTo)
	default:
		fmt.Fprintf(out, "\nno forward transition from status %q.\n", story.Status)
	}
	return nil
}

// reembedGoverningWorkflow re-stamps the story's embedded `## Workflow` from the
// AUTHORITATIVE governing workflow when the embedded copy is absent or has
// drifted from it. The embed is a derived cache that the entry gates (their
// to_status resolution), the recovery edge, and the portal read; keeping it in
// sync with the authoritative .satellites/workflows config is mechanism, not a
// workflow opinion. Idempotent: it writes a new story version only when the
// embed actually differs from the governing definition. Returns the (possibly
// rewritten) body, whether it changed, the resolved governing workflow name, and
// ok=false when no governing workflow covers the story's category — the
// ungoverned path keeps the embed as its own source (nothing authoritative to
// restamp from). Shared by `workflow embed` and the review path's self-heal so
// the stamping has one implementation.
func reembedGoverningWorkflow(ctx context.Context, story reviewStory, body, configPath, userArg string, sources []verb.WorkflowSource) (newBody string, changed bool, governing string, ok bool, err error) {
	newBody, changed, governing, ok, err = syncEmbeddedWorkflowBody(body, story.Category, sources)
	if err != nil || !ok || !changed {
		return newBody, changed, governing, ok, err
	}
	if aErr := applyStoryBody(ctx, story, newBody, configPath, userArg); aErr != nil {
		return body, false, governing, true, fmt.Errorf("write ## Workflow into story: %w", aErr)
	}
	return newBody, true, governing, true, nil
}

// syncEmbeddedWorkflowBody is the pure half of reembedGoverningWorkflow: it
// resolves the governing workflow by the story's category over the sources and
// returns body with its `## Workflow` re-stamped verbatim from the governing
// definition. changed is false when the embed already matches the governing
// definition (no rewrite needed); ok is false when no governing workflow covers
// the category (ungoverned — nothing authoritative to restamp from). No I/O, so
// the restamp transformation is fixture-testable.
func syncEmbeddedWorkflowBody(body, category string, sources []verb.WorkflowSource) (newBody string, changed bool, governing string, ok bool, err error) {
	wf, name, found := verb.ResolveGoverningWorkflow(category, sources)
	if !found {
		return body, false, "", false, nil
	}
	var srcBody string
	for _, s := range sources {
		if s.Name == name {
			srcBody = s.Body
			break
		}
	}
	yamlBlock, yErr := workflow.ExtractYAMLBlock([]byte(srcBody))
	if yErr != nil {
		return body, false, name, true, fmt.Errorf("governing workflow %q carries no ```yaml block: %w", name, yErr)
	}
	if embedded, perr := workflow.ParseBody([]byte(body)); perr == nil && embedded != nil && wf.Equivalent(embedded) {
		return body, false, name, true, nil // already in sync — no needless story version
	}
	section := "## Workflow\n\n```yaml\n" + strings.TrimRight(string(yamlBlock), "\n") + "\n```\n"
	return replaceSection(body, "## Workflow", section), true, name, true, nil
}
