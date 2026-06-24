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
	"strings"

	"github.com/bobmcallan/satellites/internal/kvtag"
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
	return nil
}
