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

	// embed STAMPS THE CHOSEN workflow (sty_cfbcc6e2): honour an existing
	// `workflow:<name>` selector; when none is set, default to the TOP-RANKED
	// category match and RECORD it as the selector (parity with the old "resolve
	// the governing one"). The recorded NAME is the authority; the stamped
	// ## Workflow is display-only.
	sources := governingWorkflowSources(configPath)
	selector := verb.WorkflowSelector(story.Tags)
	var wf *workflow.Workflow
	var name string
	if selector != "" {
		w, covers, ok := verb.ResolveByName(selector, story.Category, sources)
		if !ok {
			return fmt.Errorf("workflow embed: story %s selector %q names no workflow in the source set (fail closed) — `satellites workflow list %s` to pick a valid one", storyID, selector, storyID)
		}
		if !covers {
			return fmt.Errorf("workflow embed: story %s selector %q does not cover category %q (fail closed) — pick a matching workflow (`satellites workflow list %s`)", storyID, selector, story.Category, storyID)
		}
		wf, name = w, selector
	} else {
		w, n, ok := verb.ResolveGoverningWorkflow(story.Category, sources)
		if !ok {
			return fmt.Errorf("workflow embed: no workflow covers category %q — author one under .satellites/workflows (frontmatter applies_to) or rely on the embedded baseline (fail closed)", story.Category)
		}
		wf, name = w, n
		// Record the choice BY NAME so the engine resolves edges by name hereafter.
		if err := setWorkflowSelectorTag(ctx, story, name, configPath, userArg); err != nil {
			return fmt.Errorf("workflow embed: record selector %q: %w", name, err)
		}
	}

	// Re-stamp the display `## Workflow` from the CHOSEN source (display-only;
	// the selector is the authority). Idempotent — writes only when absent/drifted.
	var srcBody string
	for _, s := range sources {
		if s.Name == name {
			srcBody = s.Body
			break
		}
	}
	newBody, changed, sErr := stampWorkflowSection(body, wf, srcBody)
	if sErr != nil {
		return fmt.Errorf("workflow embed: %w", sErr)
	}
	if changed {
		if aErr := applyStoryBody(ctx, story, newBody, configPath, userArg); aErr != nil {
			return fmt.Errorf("workflow embed: write ## Workflow: %w", aErr)
		}
		body = newBody
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
	newBody, changed, err = stampWorkflowSection(body, wf, srcBody)
	if err != nil {
		return body, false, name, true, err
	}
	return newBody, changed, name, true, nil
}

// stampWorkflowSection re-stamps a story body's `## Workflow` display block from
// a CHOSEN workflow's source (its ```yaml block). changed is false when the
// embedded copy already matches the workflow (no needless rewrite). Pure — the
// `## Workflow` is a derived display cache; the recorded selector is the
// authority the engine enacts (sty_cfbcc6e2).
func stampWorkflowSection(body string, wf *workflow.Workflow, srcBody string) (string, bool, error) {
	yamlBlock, err := workflow.ExtractYAMLBlock([]byte(srcBody))
	if err != nil {
		return body, false, fmt.Errorf("chosen workflow carries no ```yaml block: %w", err)
	}
	if embedded, perr := workflow.ParseBody([]byte(body)); perr == nil && embedded != nil && wf.Equivalent(embedded) {
		return body, false, nil
	}
	section := "## Workflow\n\n```yaml\n" + strings.TrimRight(string(yamlBlock), "\n") + "\n```\n"
	return replaceSection(body, "## Workflow", section), true, nil
}

// setWorkflowSelectorTag records the chosen workflow on the story BY NAME — it
// drops any existing `workflow:<name>` tag, appends `workflow:<name>`, and writes
// the tags via document_upsert (patch in place). The selector is the authority
// the engine resolves edges from; this is how `workflow embed` persists the
// default top-ranked choice (sty_cfbcc6e2).
func setWorkflowSelectorTag(ctx context.Context, story reviewStory, name, configPath, userArg string) error {
	tags := make([]string, 0, len(story.Tags)+1)
	for _, t := range story.Tags {
		if _, isSel := strings.CutPrefix(strings.TrimSpace(t), verb.WorkflowSelectorTag); isSel {
			continue
		}
		tags = append(tags, t)
	}
	tags = append(tags, verb.WorkflowSelectorTag+name)
	req, err := json.Marshal(verb.DocumentUpsertRequest{ID: story.ID, Tags: &tags})
	if err != nil {
		return err
	}
	_, err = dispatchVerb(ctx, "document_upsert", req, configPath, userArg)
	return err
}
