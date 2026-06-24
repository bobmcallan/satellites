// `satellites task output` — emit a task's first-class OUTPUT document
// (epic:phases-task-outputs, B2). A task's declared OUTPUT becomes a typed,
// phase-tagged project document attached to the task's project and linked back to
// the run that produced it:
//
//	satellites task output <tsk> --name "DDD domains" --phase discovery \
//	    --body-file domains.md
//
// The DECISION (what the output says) is the agent's work; this command is thin
// MECHANISM that creates the document and writes the linkage deterministically —
// composing the existing document_upsert + ledger_append verbs. Two links make the
// output traceable (B2 AC2): a `task:<tsk_id>` KV tag on the document (traceable to
// the TASK, listable via document_list) and a `log:task_output` ledger row keyed by
// the task id (traceable to the EPISODE — it lands inside the running→complete span).

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

// taskOutputLedgerKind is the ledger kind linking an output document to the run
// that produced it. The `log:` prefix keeps the append inside the runner/executor
// role gate — a non-admin executor key may write only `log:*` rows.
const taskOutputLedgerKind = "log:task_output"

// buildOutputTags assembles the KV tag set for an output document: the `type:`
// classification (kind), an optional `phase:`, an optional `format:<id>` declaration
// (the format a consumer like the portal gates rendering on — epic:codegraph-portable),
// and a `task:<id>` back-reference. kvtag.Normalize collapses any duplicate single-valued
// key (type/phase/format) to a single last-wins value; the `task:` reference is preserved.
func buildOutputTags(kind, phase, format, taskID string) []string {
	tags := kvtag.Set(nil, "type", kind)
	if strings.TrimSpace(phase) != "" {
		tags = kvtag.Set(tags, "phase", phase)
	}
	if strings.TrimSpace(format) != "" {
		tags = kvtag.Set(tags, "format", format)
	}
	tags = append(tags, "task:"+taskID)
	return kvtag.Normalize(tags)
}

// taskOutputLedgerPayload is the structured record stamped on the link row.
type taskOutputLedgerPayload struct {
	OutputID string `json:"output_id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

func newTaskOutputCmd(configArg, userArg *string) *cobra.Command {
	var name, kind, phase, format, body, bodyFile string
	cmd := &cobra.Command{
		Use:   "output <task-id> --name <name> (--body <md> | --body-file <path>)",
		Short: "Emit a task's output as a typed, run-linked project document",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return runTaskOutput(ctx, cmd.OutOrStdout(), *configArg, *userArg, strings.TrimSpace(args[0]),
				taskOutputOpts{Name: name, Kind: kind, Phase: phase, Format: format, Body: body, BodyFile: bodyFile})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Name of the output document (required)")
	cmd.Flags().StringVar(&kind, "kind", "document", "Output classification — the KV type: tag (e.g. document, diagram)")
	cmd.Flags().StringVar(&phase, "phase", "", "Phase tag for the output (e.g. discovery)")
	cmd.Flags().StringVar(&format, "format", "", "Declared content format — the KV format: tag a consumer gates on (e.g. jgf-v1 for a codegraph)")
	cmd.Flags().StringVar(&body, "body", "", "Inline output body (markdown)")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "Path to a file whose contents become the output body")
	return cmd
}

type taskOutputOpts struct {
	Name     string
	Kind     string
	Phase    string
	Format   string
	Body     string
	BodyFile string
}

func runTaskOutput(ctx context.Context, out io.Writer, configPath, userArg, taskID string, o taskOutputOpts) error {
	if strings.TrimSpace(o.Name) == "" {
		return fmt.Errorf("task output: --name is required")
	}
	body := o.Body
	if strings.TrimSpace(o.BodyFile) != "" {
		b, err := os.ReadFile(o.BodyFile)
		if err != nil {
			return fmt.Errorf("task output: read body file: %w", err)
		}
		body = string(b)
	}
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("task output: empty body — provide --body or --body-file")
	}
	kind := strings.TrimSpace(o.Kind)
	if kind == "" {
		kind = "document"
	}

	// Resolve the task: confirm it IS a task and capture its project — the output
	// is attached there.
	getReq, _ := json.Marshal(verb.DocumentGetRequest{ID: taskID})
	raw, err := dispatchVerb(ctx, "document_get", getReq, configPath, userArg)
	if err != nil {
		return fmt.Errorf("task output: resolve %s: %w", taskID, err)
	}
	var taskResp verb.DocumentGetResponse
	if err := json.Unmarshal(raw, &taskResp); err != nil {
		return fmt.Errorf("task output: decode %s: %w", taskID, err)
	}
	if taskResp.Document.Type != taskType {
		return fmt.Errorf("task output: %s is type=%q, not a task", taskID, taskResp.Document.Type)
	}
	projectID := taskResp.Document.ProjectID
	if strings.TrimSpace(projectID) == "" {
		return fmt.Errorf("task output: task %s has no project_id", taskID)
	}
	workspaceID := taskResp.Document.WorkspaceID

	// Create the output document, attached to the task's project and tagged for
	// traceability.
	tags := buildOutputTags(kind, o.Phase, o.Format, taskID)
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
		return fmt.Errorf("task output: create output document: %w", err)
	}
	var upResp verb.DocumentUpsertResponse
	if err := json.Unmarshal(raw, &upResp); err != nil {
		return fmt.Errorf("task output: decode created output: %w", err)
	}
	outputID := upResp.Document.ID

	// Link the output to the current execution episode (best-effort): a
	// log:task_output ledger row keyed by the task id.
	payload, _ := json.Marshal(taskOutputLedgerPayload{OutputID: outputID, Name: o.Name, Kind: kind})
	logReq, _ := json.Marshal(verb.LedgerAppendRequest{
		StoryID:   taskID,
		ProjectID: projectID,
		Kind:      taskOutputLedgerKind,
		Payload:   payload,
	})
	if _, err := dispatchVerb(ctx, "ledger_append", logReq, configPath, userArg); err != nil {
		// The output document already landed; surface the link failure but do not
		// fail the command — the task: tag still makes the output traceable.
		fmt.Fprintf(out, "warning: output created but episode link failed: %v\n", err)
	}

	fmt.Fprintf(out, "%s  %s  [%s]\n", outputID, o.Name, strings.Join(tags, ", "))
	return nil
}
