// `satellites story output` — attach a story's OUTPUT as a typed, story-linked
// project document (sty_1abc1bff, epic:story-substrate-followups). It is the
// story-side peer of `satellites task output`: stories accrete artifacts (a
// summary, a codegraph, a review note, a benchmark) as their OWN documents
// instead of inlining everything into the story body.
//
//	satellites story output <sty> --name "Implementation summary" --kind summary \
//	    --body-file summary.md
//
// The DECISION (what the output says) is the agent's work; this command is thin
// MECHANISM that creates the document and writes the linkage deterministically —
// composing the existing document_upsert + ledger_append verbs, exactly as
// `task output` does. Two links make the output traceable: a `story:<sty_id>` KV
// tag on the document (traceable to the STORY, listable via document_list) and a
// `log:story_output` ledger row keyed by the story id (traceable to the run).
//
// The implementation-summary gate reads an attached `kind:summary` output, so a
// story can record WHAT it changed and WHY as a document rather than a body
// section.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/bobmcallan/satellites/internal/kvtag"
	"github.com/bobmcallan/satellites/internal/processtrace"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/spf13/cobra"
)

// storyType is the document type a story output must be attached to.
const storyType = "story"

// storyOutputLedgerKind links an output document to the story that produced it.
// The `log:` prefix keeps the append inside the executor role gate — a non-admin
// executor key may write only `log:*` rows.
const storyOutputLedgerKind = "log:story_output"

// buildStoryOutputTags assembles the KV tag set for a story output document: the
// `type:` classification (kind), an optional `phase:`, an optional `format:<id>`
// declaration, and a `story:<id>` back-reference. kvtag.Normalize collapses any
// duplicate single-valued key (type/phase/format) to a last-wins value; the
// `story:` reference is preserved. Mirrors buildOutputTags for tasks.
func buildStoryOutputTags(kind, phase, format, storyID string) []string {
	tags := kvtag.Set(nil, "type", kind)
	if strings.TrimSpace(phase) != "" {
		tags = kvtag.Set(tags, "phase", phase)
	}
	if strings.TrimSpace(format) != "" {
		tags = kvtag.Set(tags, "format", format)
	}
	tags = append(tags, "story:"+storyID)
	return kvtag.Normalize(tags)
}

// storyOutputLedgerPayload is the structured record stamped on the link row.
type storyOutputLedgerPayload struct {
	OutputID string `json:"output_id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

func newStoryOutputCmd(configArg, userArg *string) *cobra.Command {
	var name, kind, phase, format, body, bodyFile string
	cmd := &cobra.Command{
		Use:   "output <story-id> --name <name> (--body <md> | --body-file <path>)",
		Short: "Attach a story's output as a typed, story-linked project document",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return runStoryOutput(ctx, cmd.OutOrStdout(), *configArg, *userArg, strings.TrimSpace(args[0]),
				storyOutputOpts{Name: name, Kind: kind, Phase: phase, Format: format, Body: body, BodyFile: bodyFile})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Name of the output document (required)")
	cmd.Flags().StringVar(&kind, "kind", "summary", "Output classification — the KV type: tag (e.g. summary, document, diagram)")
	cmd.Flags().StringVar(&phase, "phase", "", "Phase tag for the output (e.g. discovery)")
	cmd.Flags().StringVar(&format, "format", "", "Declared content format — the KV format: tag a consumer gates on (e.g. jgf-v1)")
	cmd.Flags().StringVar(&body, "body", "", "Inline output body (markdown)")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "Path to a file whose contents become the output body")
	return cmd
}

type storyOutputOpts struct {
	Name     string
	Kind     string
	Phase    string
	Format   string
	Body     string
	BodyFile string
}

func runStoryOutput(ctx context.Context, out io.Writer, configPath, userArg, storyID string, o storyOutputOpts) error {
	if strings.TrimSpace(o.Name) == "" {
		return fmt.Errorf("story output: --name is required")
	}
	body := o.Body
	if strings.TrimSpace(o.BodyFile) != "" {
		b, err := os.ReadFile(o.BodyFile)
		if err != nil {
			return fmt.Errorf("story output: read body file: %w", err)
		}
		body = string(b)
	}
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("story output: empty body — provide --body or --body-file")
	}
	kind := strings.TrimSpace(o.Kind)
	if kind == "" {
		kind = "summary"
	}

	// Resolve the story: confirm it IS a story and capture its project — the
	// output is attached there.
	getReq, _ := json.Marshal(verb.DocumentGetRequest{ID: storyID})
	raw, err := dispatchVerb(ctx, "document_get", getReq, configPath, userArg)
	if err != nil {
		return fmt.Errorf("story output: resolve %s: %w", storyID, err)
	}
	var storyResp verb.DocumentGetResponse
	if err := json.Unmarshal(raw, &storyResp); err != nil {
		return fmt.Errorf("story output: decode %s: %w", storyID, err)
	}
	if storyResp.Document.Type != storyType {
		return fmt.Errorf("story output: %s is type=%q, not a story", storyID, storyResp.Document.Type)
	}
	projectID := storyResp.Document.ProjectID
	if strings.TrimSpace(projectID) == "" {
		return fmt.Errorf("story output: story %s has no project_id", storyID)
	}
	workspaceID := storyResp.Document.WorkspaceID

	// A summary output carries the story's ACTUAL workflow as a rendered mermaid
	// diagram (sty_135ac76d), so the closed story's implementation-summary document
	// shows the journey it took — sourced from processtrace, the same generator
	// changedoc uses, not hand-authored.
	if strings.EqualFold(kind, "summary") {
		body = appendActualWorkflowMermaid(ctx, configPath, userArg, storyID, storyResp, body, out)
	}

	// Create the output document, attached to the story's project and tagged for
	// traceability.
	tags := buildStoryOutputTags(kind, o.Phase, o.Format, storyID)
	upReq, _ := json.Marshal(verb.DocumentUpsertRequest{
		Type:        "document",
		Scope:       "project",
		ProjectID:   projectID,
		WorkspaceID: workspaceID,
		Name:        o.Name,
		Body:        body,
		Tags:        &tags,
	})
	raw, err = dispatchVerb(ctx, "document_upsert", upReq, configPath, userArg)
	if err != nil {
		return fmt.Errorf("story output: create output document: %w", err)
	}
	var upResp verb.DocumentUpsertResponse
	if err := json.Unmarshal(raw, &upResp); err != nil {
		return fmt.Errorf("story output: decode created output: %w", err)
	}
	outputID := upResp.Document.ID

	// Link the output to the story (best-effort): a log:story_output ledger row
	// keyed by the story id. The output document already landed; a link failure
	// is surfaced but not fatal — the story: tag still makes it traceable.
	payload, _ := json.Marshal(storyOutputLedgerPayload{OutputID: outputID, Name: o.Name, Kind: kind})
	logReq, _ := json.Marshal(verb.LedgerAppendRequest{
		StoryID:   storyID,
		ProjectID: projectID,
		Kind:      storyOutputLedgerKind,
		Payload:   payload,
	})
	if _, err := dispatchVerb(ctx, "ledger_append", logReq, configPath, userArg); err != nil {
		fmt.Fprintf(out, "warning: output created but story link failed: %v\n", err)
	}

	fmt.Fprintf(out, "%s  %s  [%s]\n", outputID, o.Name, strings.Join(tags, ", "))

	// Steer at the authoring moment: a dated/per-run output (a name carrying a
	// YYYY-MM-DD stamp) that is NOT the one-shot story summary is a recurring
	// artifact — by work-artifact-selection it belongs to a governed, re-runnable
	// TASK that the story CREATES, not to a fresh story-output document per run
	// (sty_23e31b56). The output already landed; this only nudges the next run
	// toward the right primitive. When no task workflow resolves, that is a config
	// gap to surface — name it, don't silently steer into a one-shot path.
	if w := datedOutputTaskSteer(o.Name, kind, projectHasTaskWorkflow(configPath)); w != "" {
		fmt.Fprintln(out, w)
	}
	return nil
}

// appendActualWorkflowMermaid enriches a summary output body with a rendered
// ```mermaid diagram of the story's ACTUAL workflow (sty_135ac76d), sourced from
// processtrace — the same reconciled declared-vs-actual projection changedoc and
// the portal trace view use, so it reflects the real traversal (including reject
// loops). No-op when the body already embeds a ```mermaid block (a hand-authored
// diagram is never duplicated), or when no governing workflow or ledger resolves
// — the output still lands, just without the diagram.
func appendActualWorkflowMermaid(ctx context.Context, configPath, userArg, storyID string, story verb.DocumentGetResponse, body string, out io.Writer) string {
	if strings.Contains(body, "```mermaid") {
		return body
	}
	wf := resolveStoryWorkflow(configPath, story.RawBody, story.Document.Category)
	if wf == nil {
		return body
	}
	entries, err := dispatchStoryLedgerEntries(ctx, storyID, configPath, userArg)
	if err != nil {
		fmt.Fprintf(out, "warning: ledger unavailable, actual-workflow diagram omitted: %v\n", err)
		return body
	}
	pt := processtrace.Reconcile(storyID, story.Document.Category, story.Document.Status, wf, entries, nil)
	return withActualWorkflowMermaid(body, processtrace.MermaidActualWorkflow(pt))
}

// withActualWorkflowMermaid appends a "## Actual workflow" section carrying the
// mermaid diagram to a summary body. Pure (no I/O): a no-op when the body already
// embeds a ```mermaid block or the diagram is empty, so it never duplicates a
// hand-authored diagram.
func withActualWorkflowMermaid(body, mermaid string) string {
	mermaid = strings.TrimSpace(mermaid)
	if mermaid == "" || strings.Contains(body, "```mermaid") {
		return body
	}
	var b strings.Builder
	b.WriteString(strings.TrimRight(body, "\n"))
	b.WriteString("\n\n## Actual workflow\n\n```mermaid\n")
	b.WriteString(mermaid)
	b.WriteString("\n```\n")
	return b.String()
}

// datedReStamp matches a YYYY-MM-DD date embedded anywhere in an output name —
// the signal that an output is produced per run rather than once.
var datedReStamp = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}\b`)

// looksDated reports whether an output name carries a YYYY-MM-DD date stamp, the
// heuristic for "this is a per-run artifact, not a one-off deliverable".
func looksDated(name string) bool {
	return datedReStamp.MatchString(name)
}

// datedOutputTaskSteer returns guidance when a story output is shaped like a
// recurring per-run artifact (dated name) but is not the one-shot summary the
// implementation-summary gate reads. Such work is a governed re-runnable TASK by
// work-artifact-selection; the story should CREATE that task rather than mint a
// dated document each run. Returns "" when there is nothing to steer. When no
// task workflow resolves, the message names the config gap instead of steering
// into a one-shot path.
func datedOutputTaskSteer(name, kind string, hasTaskWorkflow bool) string {
	if !looksDated(name) || strings.EqualFold(strings.TrimSpace(kind), "summary") {
		return ""
	}
	if !hasTaskWorkflow {
		return fmt.Sprintf("warning: output %q is dated/per-run but kind is %q — a recurring or dated output is a re-runnable TASK, "+
			"not a per-run story document (work-artifact-selection). CONFIG GAP: no task workflow resolves for category \"task\" in this project, "+
			"so there is no governed runner to create — surface the missing satellites-task-workflow rather than minting a dated document each run.",
			name, kind)
	}
	return fmt.Sprintf("warning: output %q is dated/per-run but kind is %q — a recurring or dated output is a re-runnable TASK, "+
		"not a per-run story document (work-artifact-selection). Have this story CREATE a governed task (category \"task\", resolved by "+
		"satellites-task-workflow) whose run records each dated output, instead of attaching a fresh story-output document per run.",
		name, kind)
}

// projectHasTaskWorkflow reports whether a workflow resolves for category "task"
// in this project — i.e. whether a governed re-runnable task has an enactable
// path. It reuses the same source set and resolver the governing-workflow path
// uses, so what it reports matches what `workflow embed` would resolve.
func projectHasTaskWorkflow(configPath string) bool {
	_, _, ok := verb.ResolveGoverningWorkflow("task", governingWorkflowSources(configPath))
	return ok
}
