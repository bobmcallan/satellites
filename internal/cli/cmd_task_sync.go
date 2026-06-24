// `satellites task sync` — consume global tasks from opted-in publishers and
// materialise each as a project task in the consuming project (epic:global-tasks).
//
// A task is NOT a .claude/skill file: it is a governed project object (a tsk_
// row) that `work init`, the ledger, the gated transitions, and the portal all
// key on. So — unlike `skill sync`, which reconciles files under .claude/skills/
// — task sync reconciles ROWS: it upserts the publisher's library task as a
// project type:task row, idempotent by a `forked-from:<publisher>/<name>` tag.
// Precedence mirrors skills: a same-named LOCAL task (no forked-from stamp) wins
// and is never overwritten. See task-register-design (doc_e191fa7f).

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/bobmcallan/satellites/internal/cliconfig"
	"github.com/spf13/cobra"
)

// forkedFromTagPrefix marks a consumed task with the publisher/name it was
// materialised from, so a re-sync reconciles the same row instead of minting a
// duplicate, and so precedence can tell a consumed task from a local one.
const forkedFromTagPrefix = "forked-from:"

// libTask is one library-scope task row resolved from a publisher.
type libTask struct {
	Name      string
	Body      string
	Version   int
	Publisher string
}

// projTask is one existing task in the consuming project (enough to reconcile).
type projTask struct {
	ID        string
	Name      string
	Tags      []string
	ForkedSrc string // the forked-from:<pub>/<name> value, or "" for a local task
}

// newTaskSyncCmd builds `task sync` — pull global tasks into the project.
func newTaskSyncCmd(configArg, userArg *string) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Materialise global tasks from opted-in publishers (global_publishers) as project tasks",
		Long: `sync pulls every library-scope task of each opted-in publisher
(global_publishers in satellites.toml) and materialises it as a task in this
repo's project, provenance-stamped. Re-running reconciles by forked-from tag —
no duplicates. A same-named local task (no forked-from stamp) wins and is left
untouched.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return syncTasks(ctx, cmd.OutOrStdout(), *configArg, *userArg, dryRun)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dryrun", false, "Print the create/update/skip verdict per task — dispatch nothing")
	return cmd
}

// syncTasks consumes global tasks into the consuming project. Best-effort
// callable from skill sync: with no publishers or no library tasks it is a
// silent no-op, so wiring it into the session-start sync changes nothing for a
// repo that consumes none.
func syncTasks(ctx context.Context, out io.Writer, configArg, userArg string, dryRun bool) error {
	cfg, _, err := cliconfig.Load(configArg)
	if err != nil {
		if errors.Is(err, cliconfig.ErrNotFound) {
			return nil
		}
		return err
	}
	publishers := resolveGlobalPublishers(cfg, out)
	if len(publishers) == 0 {
		return nil
	}
	consumer, err := projectIDFromConfig(configArg)
	if err != nil {
		return fmt.Errorf("task sync: resolve consuming project: %w", err)
	}
	dispatch := func(ctx context.Context, name string, req json.RawMessage) (json.RawMessage, error) {
		return dispatchVerb(ctx, name, req, configArg, userArg)
	}
	return reconcileTasks(ctx, out, dispatch, publishers, consumer, dryRun)
}

// reconcileTasks is syncTasks' core, decoupled from config/dispatch resolution
// so it is unit-testable with a fake dispatch: pull each publisher's library
// tasks, then create/update/skip against the consuming project's existing rows.
func reconcileTasks(ctx context.Context, out io.Writer, dispatch verbDispatch, publishers []string, consumer string, dryRun bool) error {
	// Pull the publishers' library tasks.
	var lib []libTask
	for _, pub := range publishers {
		pub = strings.TrimSpace(pub)
		if pub == "" {
			continue
		}
		ts, err := listLibraryTasks(ctx, dispatch, pub)
		if err != nil {
			return fmt.Errorf("task sync: publisher %q: %w", pub, err)
		}
		lib = append(lib, ts...)
	}
	if len(lib) == 0 {
		return nil
	}

	// Index the consuming project's existing tasks: forked rows (by source) so a
	// re-sync reconciles them, and local names so precedence skips them.
	existing, err := listProjectTasks(ctx, dispatch, consumer)
	if err != nil {
		return fmt.Errorf("task sync: list project tasks: %w", err)
	}
	forkedBySrc := map[string]projTask{}
	localNames := map[string]bool{}
	for _, pt := range existing {
		if pt.ForkedSrc != "" {
			forkedBySrc[pt.ForkedSrc] = pt
		} else {
			localNames[pt.Name] = true
		}
	}

	sort.Slice(lib, func(i, j int) bool { return lib[i].Name < lib[j].Name })
	for _, lt := range lib {
		src := lt.Publisher + "/" + lt.Name
		if prior, ok := forkedBySrc[src]; ok {
			// Reconcile the existing consumed row in place (by id) — keeps the
			// project's execution episodes, updates only the definition body.
			if dryRun {
				fmt.Fprintf(out, "[dry-run] update  %s  (from %s)\n", lt.Name, src)
				continue
			}
			if err := upsertConsumedTask(ctx, dispatch, consumer, prior.ID, lt, src); err != nil {
				return fmt.Errorf("task sync: update %q: %w", lt.Name, err)
			}
			fmt.Fprintf(out, "update  %s  (from %s)\n", lt.Name, src)
			continue
		}
		if localNames[lt.Name] {
			// A same-named LOCAL task wins (precedence) — never overwritten.
			fmt.Fprintf(out, "skip    %s  (local task overrides %s)\n", lt.Name, src)
			continue
		}
		if dryRun {
			fmt.Fprintf(out, "[dry-run] create  %s  (from %s)\n", lt.Name, src)
			continue
		}
		if err := upsertConsumedTask(ctx, dispatch, consumer, "", lt, src); err != nil {
			return fmt.Errorf("task sync: create %q: %w", lt.Name, err)
		}
		fmt.Fprintf(out, "create  %s  (from %s)\n", lt.Name, src)
	}
	return nil
}

// listLibraryTasks lists a publisher's library-scope tasks and fetches each body.
func listLibraryTasks(ctx context.Context, dispatch verbDispatch, publisher string) ([]libTask, error) {
	listReq, err := json.Marshal(docListRequest{Type: taskType, Scope: "library", ProjectID: publisher, Limit: 200})
	if err != nil {
		return nil, err
	}
	raw, err := dispatch(ctx, "document_list", listReq)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	var listed struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		return nil, fmt.Errorf("decode list: %w", err)
	}
	out := make([]libTask, 0, len(listed.Items))
	for _, it := range listed.Items {
		getReq, err := json.Marshal(struct {
			Name      string `json:"name"`
			Scope     string `json:"scope"`
			ProjectID string `json:"project_id"`
		}{Name: it.Name, Scope: "library", ProjectID: publisher})
		if err != nil {
			return nil, err
		}
		gotRaw, err := dispatch(ctx, "document_get", getReq)
		if err != nil {
			return nil, fmt.Errorf("get %q: %w", it.Name, err)
		}
		var got struct {
			RawBody  string `json:"raw_body"`
			Document struct {
				LatestVersion int `json:"latest_version"`
			} `json:"document"`
		}
		if err := json.Unmarshal(gotRaw, &got); err != nil {
			return nil, fmt.Errorf("decode get %q: %w", it.Name, err)
		}
		out = append(out, libTask{Name: it.Name, Body: got.RawBody, Version: got.Document.LatestVersion, Publisher: publisher})
	}
	return out, nil
}

// listProjectTasks lists the consuming project's tasks with their tags, so a
// forked-from stamp can be read off each.
func listProjectTasks(ctx context.Context, dispatch verbDispatch, projectID string) ([]projTask, error) {
	listReq, err := json.Marshal(docListRequest{Type: taskType, ProjectID: projectID, Limit: 500})
	if err != nil {
		return nil, err
	}
	raw, err := dispatch(ctx, "document_list", listReq)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	var listed struct {
		Items []struct {
			ID   string   `json:"id"`
			Name string   `json:"name"`
			Tags []string `json:"tags"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		return nil, fmt.Errorf("decode list: %w", err)
	}
	out := make([]projTask, 0, len(listed.Items))
	for _, it := range listed.Items {
		out = append(out, projTask{ID: it.ID, Name: it.Name, Tags: it.Tags, ForkedSrc: forkedFromValue(it.Tags)})
	}
	return out, nil
}

// forkedFromValue returns the forked-from:<pub>/<name> source carried in a tag
// set, or "" when the task is local (carries no such tag).
func forkedFromValue(tags []string) string {
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if strings.HasPrefix(t, forkedFromTagPrefix) {
			return strings.TrimSpace(strings.TrimPrefix(t, forkedFromTagPrefix))
		}
	}
	return ""
}

// upsertConsumedTask materialises a library task as a project task. id=="" mints
// a new row (createTask); a non-empty id reconciles the existing forked row by
// id (preserving its execution episodes). The forked-from tag stamps provenance.
func upsertConsumedTask(ctx context.Context, dispatch verbDispatch, consumer, id string, lt libTask, src string) error {
	payload := map[string]any{
		"type":       taskType,
		"scope":      "project",
		"project_id": consumer,
		"name":       lt.Name,
		"body":       lt.Body,
		"tags":       []string{forkedFromTagPrefix + src},
	}
	if id != "" {
		payload["id"] = id
	}
	req, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := dispatch(ctx, "document_upsert", req); err != nil {
		return err
	}
	return nil
}
