// `satellites story create` — create a story (type:story) from the CLI.
//
// Closes the first-touch gap (sty_5d7e6baf): an agent's governed flow is
// create-a-story → engage → edit, but story creation had no CLI surface — a
// story was only minted as a type:story document via the MCP document_upsert
// create-mode. This command is a thin wrapper over that SAME verb (no second
// write path): the entry reviewer still gates the body on the first gated
// transition. --project defaults to the configured project in satellites.toml,
// so an agent working inside its repo need not pass it.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/bobmcallan/satellites/internal/cliconfig"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/spf13/cobra"
)

// storyCreateOpts is the flag surface for `story create`.
type storyCreateOpts struct {
	Name     string
	Project  string
	Body     string
	Category string
	Priority string
	Parent   string
	Status   string
	Tags     []string
}

func newStoryCreateCmd(configArg, userArg *string) *cobra.Command {
	var o storyCreateOpts
	cmd := &cobra.Command{
		Use:   "create --name <title> [--project <id>]",
		Short: "Create a story (type:story) via the document_upsert create path",
		Long: `create mints a new story in a project through the same document_upsert
story-create verb the MCP surface uses — there is no second write path, and the
story's entry reviewer still gates its body on the first gated transition.

--name is required. --project defaults to project_id in satellites.toml, so an
agent working inside its repo can omit it. The rest map to the story's metadata.

An anchor/epic (a story that groups children and carries no executable work of
its own) must be category "parent" — that resolves satellites-parent-workflow,
whose backlog→done edge is the parent-close gate. Authored as any other category
it has no enactable close path. Creating a child with --parent pointed at a
non-parent anchor warns and prints the in-place fix; to re-categorize an existing
anchor use document_upsert (id + category + the workflow:satellites-parent-workflow
selector tag) + workflow embed — NOT a second story create, which mints a duplicate.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runStoryCreate(cmd.Context(), cmd.OutOrStdout(), *configArg, *userArg, o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.Name, "name", "", "Story title (required)")
	f.StringVar(&o.Project, "project", "", "Project id (default: project_id from satellites.toml)")
	f.StringVar(&o.Body, "body", "", "Story body markdown")
	f.StringSliceVar(&o.Tags, "tags", nil, "Tags, e.g. epic:foo,area:bar")
	f.StringVar(&o.Category, "category", "", "Category (feature|bug|improvement|chore|…)")
	f.StringVar(&o.Priority, "priority", "", "Priority (critical|high|medium|low)")
	f.StringVar(&o.Parent, "parent", "", "Parent story id (e.g. an epic)")
	f.StringVar(&o.Status, "status", "", "Initial status (default: backlog)")
	return cmd
}

// storyCreateRequest builds the document_upsert story-create request — the SAME
// verb the MCP create-mode uses. Pointer-typed metadata is set only when the
// flag was provided, so an unset field is left to the server's default rather
// than written as an explicit empty. Pure, for tests.
func storyCreateRequest(o storyCreateOpts) verb.DocumentUpsertRequest {
	req := verb.DocumentUpsertRequest{
		Type:      "story",
		ProjectID: strings.TrimSpace(o.Project),
		Name:      strings.TrimSpace(o.Name),
		Body:      o.Body,
	}
	if len(o.Tags) > 0 {
		tags := append([]string(nil), o.Tags...)
		req.Tags = &tags
	}
	if s := strings.TrimSpace(o.Category); s != "" {
		req.Category = &s
	}
	if s := strings.TrimSpace(o.Priority); s != "" {
		req.Priority = &s
	}
	if s := strings.TrimSpace(o.Parent); s != "" {
		req.ParentID = &s
	}
	if s := strings.TrimSpace(o.Status); s != "" {
		req.Status = &s
	}
	return req
}

func runStoryCreate(ctx context.Context, out io.Writer, configPath, userArg string, o storyCreateOpts) error {
	if strings.TrimSpace(o.Name) == "" {
		return fmt.Errorf("story create: --name is required")
	}
	if strings.TrimSpace(o.Project) == "" {
		cfg, _, err := cliconfig.Load(configPath)
		if err != nil {
			return fmt.Errorf("story create: load config to resolve project: %w", err)
		}
		o.Project = strings.TrimSpace(cfg.ProjectID)
	}
	if strings.TrimSpace(o.Project) == "" {
		return fmt.Errorf("story create: no project — pass --project or set project_id in satellites.toml")
	}

	req, err := json.Marshal(storyCreateRequest(o))
	if err != nil {
		return err
	}
	raw, err := dispatchVerb(ctx, "document_upsert", req, configPath, userArg)
	if err != nil {
		return fmt.Errorf("story create: %w", err)
	}
	var resp verb.DocumentUpsertResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("story create: decode response: %w", err)
	}
	fmt.Fprintf(out, "created %s  %s\n", resp.Document.ID, resp.Document.Name)

	// Steer at the earliest authoring moment: parenting a child to an anchor that
	// is NOT category "parent" is the first time the anchor "gains children", so
	// it is where mis-categorization is cheapest to catch (sty_b83fae1d).
	// Best-effort — a resolve failure must not fail the (already successful)
	// create.
	if parent := strings.TrimSpace(o.Parent); parent != "" {
		if w := parentAnchorWarning(ctx, configPath, userArg, parent); w != "" {
			fmt.Fprintln(out, w)
		}
	}
	return nil
}

// parentAnchorWarning resolves a child's parent and, when that parent is shaped
// like an anchor but is not category "parent", returns the steer toward
// re-categorizing it. Empty on any resolve failure (best-effort).
func parentAnchorWarning(ctx context.Context, configPath, userArg, parentID string) string {
	req, err := json.Marshal(verb.DocumentGetRequest{ID: parentID})
	if err != nil {
		return ""
	}
	raw, err := dispatchVerb(ctx, "document_get", req, configPath, userArg)
	if err != nil {
		return ""
	}
	var resp verb.DocumentGetResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return ""
	}
	// The parent now has at least this child, so hasChildren is true.
	return anchorMiscategorizationWarning(parentID, resp.Document.Category, true)
}
