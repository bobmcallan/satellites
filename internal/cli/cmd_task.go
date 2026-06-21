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

	"github.com/bobmcallan/satellites/internal/taskepisode"
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
	task.AddCommand(newTaskReviewCmd(&configArg, &userArg))
	task.AddCommand(newTaskExecutionsCmd(&configArg, &userArg))
	task.AddCommand(newTaskRunCmd(&configArg, &userArg))

	register(task)
}

// newTaskReviewCmd is `satellites task status_transition` — the task-namespaced
// alias of `story status_transition`. It drives a task gate through the SAME
// dispatch (runReview): it resolves the governing task workflow (by category
// 'task') and reuses the gate-match / reviewer-only enforcement. A task is
// re-runnable, so a `complete` task is re-opened by running the entry gate again
// (the workflow's complete→running re-arm edge), which begins a fresh episode.
func newTaskReviewCmd(configArg, userArg *string) *cobra.Command {
	var (
		claudeBin    string
		worktreeRoot string
		skill        string
		checkpoint   bool
	)
	cmd := &cobra.Command{
		Use:   "status_transition --skill <gate> <task-id>",
		Short: "Run a named reviewer gate skill against a task, client-side (drives ready→running→complete; re-runnable)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return runReview(ctx, reviewOpts{
				StoryID:      strings.TrimSpace(args[0]),
				ConfigPath:   *configArg,
				UserArg:      *userArg,
				ClaudeBin:    claudeBin,
				WorktreeRoot: worktreeRoot,
				Skill:        strings.TrimSpace(skill),
				Checkpoint:   checkpoint,
				Stdout:       cmd.OutOrStdout(),
				Stderr:       cmd.ErrOrStderr(),
			})
		},
	}
	cmd.Flags().StringVar(&claudeBin, "claude-bin", "", "Path to the claude binary (defaults to $SATELLITES_CLAUDE_BIN or `claude` on PATH).")
	cmd.Flags().StringVar(&worktreeRoot, "worktree", "", "Worktree root the gate runs against (default: current directory).")
	cmd.Flags().StringVar(&skill, "skill", "", "Name the gate skill to run against the task (required unless --checkpoint).")
	cmd.Flags().BoolVar(&checkpoint, "checkpoint", false, "Advance an ungated trigger:checkpoint edge — a deliberate executor move. Mutually exclusive with --skill.")
	return cmd
}

func newTaskExecutionsCmd(configArg, userArg *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "executions <task-id>",
		Short: "List a task's execution episodes (each ready/complete→running…→terminal run) oldest-first",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return runTaskExecutions(ctx, cmd.OutOrStdout(), *configArg, *userArg, strings.TrimSpace(args[0]))
		},
	}
	return cmd
}

func runTaskExecutions(ctx context.Context, out io.Writer, configPath, userArg, taskID string) error {
	// Page the full ledger oldest-first.
	var rows []taskepisode.Row
	cursor := ""
	for {
		req, err := json.Marshal(verb.LedgerListRequest{StoryID: taskID, Limit: 200, Cursor: cursor})
		if err != nil {
			return err
		}
		raw, err := dispatchVerb(ctx, "ledger_list", req, configPath, userArg)
		if err != nil {
			return fmt.Errorf("task executions: %w", err)
		}
		var resp verb.LedgerListResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return fmt.Errorf("task executions: decode: %w", err)
		}
		for _, e := range resp.Entries {
			to := ""
			if e.Kind == "status_transition" {
				var p struct {
					To string `json:"to_status"`
				}
				_ = json.Unmarshal(e.Payload, &p)
				to = p.To
			}
			rows = append(rows, taskepisode.Row{Kind: e.Kind, To: to, Created: e.CreatedAt})
		}
		if resp.NextCursor == "" {
			break
		}
		cursor = resp.NextCursor
	}

	episodes := taskepisode.Project(rows)
	if len(episodes) == 0 {
		fmt.Fprintln(out, "(no executions — the task has not been run)")
		return nil
	}
	for _, ep := range episodes {
		status := "running (open)"
		end := "—"
		if !ep.Open() {
			status = ep.EndTo
			end = ep.End.Format("2006-01-02 15:04:05Z")
		}
		fmt.Fprintf(out, "run-%d  %s → %s  [%s]  (%d ledger rows)\n",
			ep.Run, ep.Start.Format("2006-01-02 15:04:05Z"), end, status, ep.Rows)
	}
	return nil
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
