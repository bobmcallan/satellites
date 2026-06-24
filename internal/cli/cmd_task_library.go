// `satellites task library list` + `satellites task copy <name>` — the
// browse-and-pick half of the global-tasks library (sty_9abf6814,
// epic:library-discovery). `task sync` is the bulk, config-driven path (it
// consumes every library task of each global_publisher); these two let an agent
// REVIEW what is published and COPY one chosen task into the current project
// WITHOUT editing global_publishers or knowing the publisher's project_id.
//
// Copy reuses the same materialisation `task sync` uses (listLibraryTasks →
// listProjectTasks → upsertConsumedTask), so a copied task is a live, governed
// tsk_ row stamped with the same forked-from provenance — there is no parallel
// write path.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// libTaskRef is one library task as the browse list shows it (no body fetched).
type libTaskRef struct {
	Name      string
	Publisher string
	Headline  string
}

// newTaskLibraryCmd builds `task library` — the browse surface over published
// library tasks. Today it carries `list`; copy lives at the top level as
// `task copy` to read as a verb.
func newTaskLibraryCmd(configArg, userArg *string) *cobra.Command {
	lib := &cobra.Command{
		Use:   "library",
		Short: "Browse the global task library (published, library-scope tasks)",
	}
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List published library tasks (name, publisher, headline) — independent of global_publishers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return runTaskLibraryList(ctx, cmd.OutOrStdout(), *configArg, *userArg)
		},
	}
	lib.AddCommand(listCmd)
	return lib
}

// newTaskCopyCmd builds `task copy <name> [--from <publisher>]` — materialise one
// published library task into the current project.
func newTaskCopyCmd(configArg, userArg *string) *cobra.Command {
	var from string
	cmd := &cobra.Command{
		Use:   "copy <name>",
		Short: "Copy a published library task into this project as a live, governed task",
		Long: `copy materialises ONE published library task (browse them with
` + "`satellites task library list`" + `) into this repo's project as a live tsk_ row,
provenance-stamped with forked-from. It reuses the same write ` + "`task sync`" + ` uses,
needs no global_publishers entry, and re-copying reconciles the existing row in
place. A same-named LOCAL task wins (precedence) and is never overwritten. Pass
--from when more than one publisher publishes a task of the same name.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return runTaskCopy(ctx, cmd.OutOrStdout(), *configArg, *userArg, strings.TrimSpace(args[0]), strings.TrimSpace(from))
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "Publisher project_id to disambiguate when the same task name is published by more than one")
	return cmd
}

// listAllLibraryTaskRefs lists EVERY publisher's library-scope tasks (no
// project_id filter). A scope:library request keeps the rows that
// dropForeignLibraryTasks strips from project-scope listings.
func listAllLibraryTaskRefs(ctx context.Context, dispatch verbDispatch) ([]libTaskRef, error) {
	listReq, err := json.Marshal(docListRequest{Type: taskType, Scope: "library", Limit: 500})
	if err != nil {
		return nil, err
	}
	raw, err := dispatch(ctx, "document_list", listReq)
	if err != nil {
		return nil, fmt.Errorf("list library tasks: %w", err)
	}
	var listed struct {
		Items []struct {
			Name      string `json:"name"`
			ProjectID string `json:"project_id"`
			Headline  string `json:"headline"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		return nil, fmt.Errorf("decode library tasks: %w", err)
	}
	refs := make([]libTaskRef, 0, len(listed.Items))
	for _, it := range listed.Items {
		refs = append(refs, libTaskRef{Name: it.Name, Publisher: it.ProjectID, Headline: it.Headline})
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Name != refs[j].Name {
			return refs[i].Name < refs[j].Name
		}
		return refs[i].Publisher < refs[j].Publisher
	})
	return refs, nil
}

func runTaskLibraryList(ctx context.Context, out io.Writer, configArg, userArg string) error {
	dispatch := func(ctx context.Context, name string, req json.RawMessage) (json.RawMessage, error) {
		return dispatchVerb(ctx, name, req, configArg, userArg)
	}
	refs, err := listAllLibraryTaskRefs(ctx, dispatch)
	if err != nil {
		return fmt.Errorf("task library list: %w", err)
	}
	if len(refs) == 0 {
		fmt.Fprintln(out, "no library tasks (publish one with `satellites task publish`)")
		return nil
	}
	for _, r := range refs {
		headline := r.Headline
		if headline == "" {
			headline = "-"
		}
		fmt.Fprintf(out, "%-28s  %-16s  %s\n", r.Name, r.Publisher, headline)
	}
	return nil
}

// runTaskCopy resolves config/consumer, then delegates to the dispatch-injectable
// copyLibraryTask core.
func runTaskCopy(ctx context.Context, out io.Writer, configArg, userArg, name, from string) error {
	dispatch := func(ctx context.Context, vname string, req json.RawMessage) (json.RawMessage, error) {
		return dispatchVerb(ctx, vname, req, configArg, userArg)
	}
	consumer, err := projectIDFromConfig(configArg)
	if err != nil {
		return fmt.Errorf("task copy: %w", err)
	}
	return copyLibraryTask(ctx, out, dispatch, consumer, name, from)
}

// copyLibraryTask resolves a library task by name (disambiguated by --from), then
// materialises it into the consuming project via the shared sync write. Decoupled
// from config/dispatch resolution so it is unit-testable with a fake dispatch.
func copyLibraryTask(ctx context.Context, out io.Writer, dispatch verbDispatch, consumer, name, from string) error {
	// Resolve which publisher publishes this name.
	refs, err := listAllLibraryTaskRefs(ctx, dispatch)
	if err != nil {
		return fmt.Errorf("task copy: %w", err)
	}
	var matches []libTaskRef
	for _, r := range refs {
		if r.Name == name && (from == "" || r.Publisher == from) {
			matches = append(matches, r)
		}
	}
	if len(matches) == 0 {
		if from != "" {
			return fmt.Errorf("task copy: no library task named %q published by %q (try `satellites task library list`)", name, from)
		}
		return fmt.Errorf("task copy: no library task named %q (try `satellites task library list`)", name)
	}
	if len(matches) > 1 {
		pubs := make([]string, 0, len(matches))
		for _, m := range matches {
			pubs = append(pubs, m.Publisher)
		}
		return fmt.Errorf("task copy: %q is published by more than one publisher (%s) — pass --from <publisher>", name, strings.Join(pubs, ", "))
	}
	publisher := matches[0].Publisher

	// Fetch the task body via the same library lister sync uses.
	libTasks, err := listLibraryTasks(ctx, dispatch, publisher)
	if err != nil {
		return fmt.Errorf("task copy: fetch %q from %q: %w", name, publisher, err)
	}
	var lt *libTask
	for i := range libTasks {
		if libTasks[i].Name == name {
			lt = &libTasks[i]
			break
		}
	}
	if lt == nil {
		return fmt.Errorf("task copy: library task %q vanished from publisher %q", name, publisher)
	}

	// Reconcile against the consuming project, mirroring sync's create/update/skip.
	existing, err := listProjectTasks(ctx, dispatch, consumer)
	if err != nil {
		return fmt.Errorf("task copy: list project tasks: %w", err)
	}
	src := publisher + "/" + name
	for _, pt := range existing {
		if pt.ForkedSrc == src {
			if err := upsertConsumedTask(ctx, dispatch, consumer, pt.ID, *lt, src); err != nil {
				return fmt.Errorf("task copy: update %q: %w", name, err)
			}
			fmt.Fprintf(out, "updated  %s  (%s)  from %s\n", name, pt.ID, src)
			return nil
		}
		if pt.ForkedSrc == "" && pt.Name == name {
			return fmt.Errorf("task copy: a local task %q already exists in this project — not overwritten (local precedence)", name)
		}
	}
	if err := upsertConsumedTask(ctx, dispatch, consumer, "", *lt, src); err != nil {
		return fmt.Errorf("task copy: create %q: %w", name, err)
	}
	// Re-list to surface the new tsk_ id.
	id := ""
	if after, lerr := listProjectTasks(ctx, dispatch, consumer); lerr == nil {
		for _, pt := range after {
			if pt.ForkedSrc == src {
				id = pt.ID
				break
			}
		}
	}
	fmt.Fprintf(out, "copied   %s  %s  from %s\n", name, id, src)
	return nil
}
