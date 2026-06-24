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
agent working inside its repo can omit it. The rest map to the story's metadata.`,
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
	return nil
}
