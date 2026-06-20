// `satellites task` — top-level project task commands (epic:project-tasks). A
// task (type:task, tsk_) is a re-runnable, reviewer-gated work item, a peer to
// stories. This group is read-only for now: `get` reads one task, `list`
// enumerates a project's tasks. Tasks are created/patched via document_upsert
// ({type:"task"}); their gated transitions run through `story status_transition`
// once the task workflow + gates land (epic-order:2/3).

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

// taskType is the documents.type discriminator for a task. Declared as a local
// literal (not the domain package's document.TypeTask) so the CLI layer keeps no
// import on the domain package — see TestNoSubstrateDomainImports.
const taskType = "task"

func init() {
	var (
		configArg string
		userArg   string
	)

	task := &cobra.Command{
		Use:   "task",
		Short: "Top-level project task commands (read a task; list a project's tasks)",
	}
	task.PersistentFlags().StringVar(&configArg, "config", "", "Path to satellites.toml (overrides $SATELLITES_CONFIG / .satellites/satellites.toml walk-up).")
	task.PersistentFlags().StringVar(&userArg, "user", "", "Caller user id (overrides $SATELLITES_USER_ID). Stamped onto verbs when dispatching in-process.")

	task.AddCommand(newTaskGetCmd(&configArg, &userArg))
	task.AddCommand(newTaskListCmd(&configArg, &userArg))

	register(task)
}

func newTaskGetCmd(configArg, userArg *string) *cobra.Command {
	var withBody bool
	cmd := &cobra.Command{
		Use:   "get <task-id>",
		Short: "Read a task's server-side state: status, priority, tags, parent, title",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTaskGet(cmd.Context(), cmd.OutOrStdout(), *configArg, *userArg, args[0], withBody)
		},
	}
	cmd.Flags().BoolVar(&withBody, "body", false, "Also print the full task body after the metadata")
	return cmd
}

func runTaskGet(ctx context.Context, out io.Writer, configPath, userArg, taskID string, withBody bool) error {
	req, err := json.Marshal(verb.DocumentGetRequest{ID: taskID})
	if err != nil {
		return err
	}
	raw, err := dispatchVerb(ctx, "document_get", req, configPath, userArg)
	if err != nil {
		return fmt.Errorf("task get: resolve %s: %w", taskID, err)
	}
	var resp verb.DocumentGetResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("task get: decode %s: %w", taskID, err)
	}
	if resp.Document.Type != taskType {
		return fmt.Errorf("task get: %s is type=%q, not a task", taskID, resp.Document.Type)
	}
	d := resp.Document
	fmt.Fprintf(out, "id:        %s\n", d.ID)
	fmt.Fprintf(out, "title:     %s\n", d.Name)
	fmt.Fprintf(out, "status:    %s\n", d.Status)
	fmt.Fprintf(out, "priority:  %s\n", d.Priority)
	if d.ParentID != "" {
		fmt.Fprintf(out, "parent:    %s\n", d.ParentID)
	}
	if len(d.Tags) > 0 {
		fmt.Fprintf(out, "tags:      %s\n", strings.Join(d.Tags, ", "))
	}
	fmt.Fprintf(out, "updated:   %s\n", d.UpdatedAt.Format("2006-01-02 15:04:05Z"))
	if withBody {
		body := resp.RawBody
		if body == "" && len(resp.Versions) > 0 {
			body = resp.Versions[len(resp.Versions)-1].Body
		}
		fmt.Fprintf(out, "\n%s\n", body)
	}
	return nil
}

func newTaskListCmd(configArg, userArg *string) *cobra.Command {
	var projectID, status string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List a project's tasks (id, status, priority, title)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTaskList(cmd.Context(), cmd.OutOrStdout(), *configArg, *userArg, projectID, status)
		},
	}
	cmd.Flags().StringVar(&projectID, "project-id", "", "Project to list tasks for (defaults to the repo's satellites.toml project_id).")
	cmd.Flags().StringVar(&status, "status", "", "Filter by status (e.g. ready, running, complete).")
	return cmd
}

func runTaskList(ctx context.Context, out io.Writer, configPath, userArg, projectID, status string) error {
	if strings.TrimSpace(projectID) == "" {
		pid, err := projectIDFromConfig(configPath)
		if err != nil {
			return fmt.Errorf("task list: %w", err)
		}
		projectID = pid
	}
	req, err := json.Marshal(verb.DocumentListRequest{
		Type:      taskType,
		ProjectID: projectID,
		Status:    status,
		Limit:     200,
	})
	if err != nil {
		return err
	}
	raw, err := dispatchVerb(ctx, "document_list", req, configPath, userArg)
	if err != nil {
		return fmt.Errorf("task list: %w", err)
	}
	var resp verb.DocumentListResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("task list: decode: %w", err)
	}
	if len(resp.Items) == 0 {
		fmt.Fprintln(out, "(no tasks)")
		return nil
	}
	for _, d := range resp.Items {
		fmt.Fprintf(out, "%s  %-10s  %-8s  %s\n", d.ID, d.Status, d.Priority, d.Name)
	}
	return nil
}
