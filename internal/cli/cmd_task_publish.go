// `satellites task publish` — promote one .satellites/tasks/ task into the
// shared library under this repo's publisher namespace (epic:global-tasks).
//
// A global task reuses the skill register's library machinery verbatim: the same
// scope:library surface, the same provenance stamp (publisher + repo + commit),
// and the same headless dispatch (dispatchVerb authenticates with the
// credential-store API key, nothing prompts) — so a CI step can publish on merge.
// The only differences from `skill publish` are the source dir (.satellites/tasks/)
// and that a task is NOT a behaviour kind, so it carries no review attestation.
// See task-register-design (doc_e191fa7f).

package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// newTaskPublishCmd builds the `task publish <name>` command, the task analog of
// `skill publish`. It shares publishSkill's core with kind="tasks".
func newTaskPublishCmd(configArg, userArg *string) *cobra.Command {
	var (
		dryRun       bool
		skipReview   bool
		all          bool
		changedSince string
	)
	cmd := &cobra.Command{
		Use:   "publish [name]",
		Short: "Promote .satellites/tasks/ task(s) into the shared library under this project's publisher namespace",
		Long: `publish promotes a task (or a batch) into the shared library.

  publish <name>                 one named task
  publish --all                  every task under .satellites/tasks/
  publish --changed-since <ref>  only tasks whose files differ from <ref>

A task reuses the skill register's library/provenance/headless-dispatch path; it
declares level: global in frontmatter to land on scope:library. A consumer opts
into the publisher via global_publishers and materialises it as a project task on
sync. A batch reuses the single-publish path per task, prints a per-task outcome
line, and exits non-zero if any task fails. --dryrun and --skip-review apply
across the batch.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			selectors := 0
			if len(args) == 1 {
				selectors++
			}
			if all {
				selectors++
			}
			if strings.TrimSpace(changedSince) != "" {
				selectors++
			}
			switch {
			case selectors == 0:
				return fmt.Errorf("publish: provide a task <name>, --all, or --changed-since <ref>")
			case selectors > 1:
				return fmt.Errorf("publish: <name>, --all, and --changed-since are mutually exclusive")
			case len(args) == 1:
				return publishSkill(ctx, out, args[0], "tasks", *configArg, *userArg, dryRun, skipReview)
			}
			var (
				names []string
				err   error
			)
			if all {
				names, err = allPublishableNames("tasks", *configArg)
			} else {
				names, err = changedPublishableNames(changedSince, "tasks", *configArg)
			}
			if err != nil {
				return err
			}
			return publishBatch(ctx, out, names, "tasks", *configArg, *userArg, dryRun, skipReview)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dryrun", false, "Print the identity, version, and provenance that would be published — dispatch nothing")
	cmd.Flags().BoolVar(&skipReview, "skip-review", false, "Skip the strict content review (drift-prone reference check)")
	cmd.Flags().BoolVar(&all, "all", false, "Publish every task under .satellites/tasks/")
	cmd.Flags().StringVar(&changedSince, "changed-since", "", "Publish only tasks whose files differ from the given git ref")
	return cmd
}
