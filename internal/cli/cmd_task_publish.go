// `satellites task publish` — copy a developed/tested PROJECT task into the
// shared library under this repo's publisher namespace (epic:global-tasks).
//
// The publisher model for tasks is deliberately lighter than for skills: a task
// is authored and tested as an ordinary project task (a tsk_ row), then this
// verb copies a provenance-stamped version of that row into scope:library. No
// separate publisher repo, no .satellites/tasks/ file, no CI secret to stand up —
// the publisher is simply the project that owns the task. It reuses the library
// provenance stamp and the scope:library routing guard (routesToCreateTask), and
// is headless (dispatchVerb), so it still runs from CI or a local shell. A
// consumer materialises it back via `task sync` (the story-2 consume path).

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

// newTaskPublishCmd builds `task publish <task-id|name>` — copy a project task
// into the library.
func newTaskPublishCmd(configArg, userArg *string) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "publish <task-id|name>",
		Short: "Copy a project task into the shared library under this project's publisher namespace",
		Long: `publish copies a developed/tested PROJECT task into the shared library.

  publish <tsk_id>     publish the task with that id
  publish <name>       publish the project task with that exact title

The task is resolved in this repo's project (satellites.toml), provenance-stamped
(publisher + repo + commit), and upserted at scope:library under the publisher
namespace. Re-publishing the same task updates the library copy (idempotent by
name). A consumer opts in via global_publishers and materialises it with
` + "`task sync`" + `.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			publisher, err := projectIDFromConfig(*configArg)
			if err != nil {
				return err
			}
			dispatch := func(ctx context.Context, name string, req json.RawMessage) (json.RawMessage, error) {
				return dispatchVerb(ctx, name, req, *configArg, *userArg)
			}
			return publishProjectTask(ctx, cmd.OutOrStdout(), dispatch, args[0], publisher, dryRun)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dryrun", false, "Print the identity and provenance that would be published — dispatch nothing")
	return cmd
}

// publishProjectTask resolves a project task by id or name, stamps library
// provenance into a copy of its body, and upserts it at scope:library under the
// publisher namespace. The dispatch seam makes the core unit-testable.
func publishProjectTask(ctx context.Context, out io.Writer, dispatch verbDispatch, ref, publisher string, dryRun bool) error {
	wantType, noun, _ := publishableKind("tasks")
	name, body, tags, err := resolveProjectTask(ctx, dispatch, ref, publisher, wantType)
	if err != nil {
		return err
	}
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("publish: %s %q has an empty body — nothing to publish", noun, name)
	}

	prov := libraryProvenance{
		Publisher: publisher,
		Repo:      gitOutput("remote", "get-url", "origin"),
		Commit:    gitOutput("rev-parse", "HEAD"),
	}
	stamp, err := renderLibraryStamp(prov)
	if err != nil {
		return fmt.Errorf("publish: render provenance: %w", err)
	}
	libBody := injectLibraryStamp(body, stamp)
	// Publishing an ORIGINAL: drop any consumer forked-from stamp so the library
	// copy is not marked as itself consumed-from-elsewhere.
	libTags := dropForkedFrom(tags)

	if dryRun {
		fmt.Fprintf(out, "[dryrun] would publish %s/%s\n", publisher, name)
		fmt.Fprintf(out, "[dryrun]   provenance: %s\n", stamp)
		fmt.Fprintln(out, "[dryrun] nothing dispatched")
		return nil
	}

	payload := map[string]any{
		"type":       wantType,
		"scope":      "library",
		"project_id": publisher,
		"name":       name,
		"body":       libBody,
	}
	if len(libTags) > 0 {
		payload["tags"] = libTags
	}
	req, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	resp, err := dispatch(ctx, "document_upsert", req)
	if err != nil {
		return fmt.Errorf("publish %s: %w", name, err)
	}
	fmt.Fprintf(out, "%s → library/%s/%s (%s)\n", name, publisher, name, summariseUploadResp(resp))
	return nil
}

// resolveProjectTask reads a project task by id (tsk_…) or exact name in the
// publisher project, returning its name, body, and tags. A name that matches no
// task — or more than one — is an error (publish needs an unambiguous target).
func resolveProjectTask(ctx context.Context, dispatch verbDispatch, ref, publisher, wantType string) (name, body string, tags []string, err error) {
	id := strings.TrimSpace(ref)
	if !strings.HasPrefix(id, "tsk_") {
		id, err = projectTaskIDByName(ctx, dispatch, ref, publisher)
		if err != nil {
			return "", "", nil, err
		}
	}
	getReq, err := json.Marshal(verb.DocumentGetRequest{ID: id})
	if err != nil {
		return "", "", nil, err
	}
	raw, err := dispatch(ctx, "document_get", getReq)
	if err != nil {
		return "", "", nil, fmt.Errorf("publish: resolve %s: %w", id, err)
	}
	var resp verb.DocumentGetResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", "", nil, fmt.Errorf("publish: decode %s: %w", id, err)
	}
	if resp.Document.Type != wantType {
		return "", "", nil, fmt.Errorf("publish: %s is type=%q, not a task", id, resp.Document.Type)
	}
	body = resp.RawBody
	if body == "" && len(resp.Versions) > 0 {
		body = resp.Versions[len(resp.Versions)-1].Body
	}
	return resp.Document.Name, body, resp.Document.Tags, nil
}

// projectTaskIDByName finds the single project task whose title equals ref.
func projectTaskIDByName(ctx context.Context, dispatch verbDispatch, ref, publisher string) (string, error) {
	listReq, err := json.Marshal(docListRequest{Type: taskType, ProjectID: publisher, Limit: 500})
	if err != nil {
		return "", err
	}
	raw, err := dispatch(ctx, "document_list", listReq)
	if err != nil {
		return "", fmt.Errorf("publish: list project tasks: %w", err)
	}
	var listed struct {
		Items []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		return "", fmt.Errorf("publish: decode task list: %w", err)
	}
	var matches []string
	for _, it := range listed.Items {
		if it.Name == ref {
			matches = append(matches, it.ID)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("publish: no project task titled %q (pass a tsk_ id to disambiguate)", ref)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("publish: %d project tasks are titled %q — pass a tsk_ id instead", len(matches), ref)
	}
}

// dropForkedFrom returns tags with any forked-from:<…> stamp removed.
func dropForkedFrom(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if strings.HasPrefix(strings.TrimSpace(t), forkedFromTagPrefix) {
			continue
		}
		out = append(out, t)
	}
	return out
}
