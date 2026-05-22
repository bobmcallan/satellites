// Story subcommands wrap the story_* verbs in typed flags so operators
// don't have to hand-craft JSON for everyday CLI use. Each subcommand
// builds its verb request and routes through dispatchVerb — same
// config-aware path as `satellites exec`.
//
// Mutations (create, update) are the primary surface for stories per
// V3/V4 practice; the portal stays read-only.

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/bobmcallan/satellites/internal/cliconfig"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/spf13/cobra"
)

// resolveProjectID returns flagValue when set; otherwise the project_id
// persisted at .satellites/satellites.toml; otherwise attempts a
// self-heal (git remote → project_match → TOML write) before
// surfacing the documented "project_id not defined" error.
func resolveProjectID(flagValue, configPath, userArg string) (string, error) {
	if v := strings.TrimSpace(flagValue); v != "" {
		return v, nil
	}
	cfg, resolvedPath, err := cliconfig.Load(configPath)
	if err != nil && !errors.Is(err, cliconfig.ErrNotFound) {
		return "", err
	}
	if v := strings.TrimSpace(cfg.ProjectID); v != "" {
		return v, nil
	}
	if healed, healErr := selfHealProjectID(context.Background(), cfg, resolvedPath, userArg); healErr == nil {
		return healed, nil
	}
	return "", fmt.Errorf("error project_id not defined")
}

func init() {
	var (
		configArg string
		userArg   string
	)

	story := &cobra.Command{
		Use:   "story",
		Short: "Manage stories under a project (create / list / get / update)",
	}
	story.PersistentFlags().StringVar(&configArg, "config", "", "Path to satellites.toml (overrides $SATELLITES_CONFIG / .satellites/satellites.toml walk-up).")
	story.PersistentFlags().StringVar(&userArg, "user", "", "Caller user id (overrides $SATELLITES_USER_ID). Stamped onto verbs when dispatching in-process.")

	story.AddCommand(newStoryCreateCmd(&configArg, &userArg))
	story.AddCommand(newStoryListCmd(&configArg, &userArg))
	story.AddCommand(newStoryGetCmd(&configArg, &userArg))
	story.AddCommand(newStoryUpdateCmd(&configArg, &userArg))

	register(story)
}

func newStoryCreateCmd(configArg, userArg *string) *cobra.Command {
	var req verb.StoryCreateRequest
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a story under a project",
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, err := resolveProjectID(req.ProjectID, *configArg, *userArg)
			if err != nil {
				return err
			}
			req.ProjectID = pid
			if strings.TrimSpace(req.Title) == "" {
				return fmt.Errorf("--title required")
			}
			body, err := json.Marshal(req)
			if err != nil {
				return err
			}
			return runStoryVerb(cmd, "story_create", body, *configArg, *userArg)
		},
	}
	cmd.Flags().StringVar(&req.ProjectID, "project-id", "", "Project id (falls back to project_id in satellites.toml)")
	cmd.Flags().StringVar(&req.ParentID, "parent-id", "", "Parent story id (optional)")
	cmd.Flags().StringVar(&req.Title, "title", "", "Story title (required)")
	cmd.Flags().StringVar(&req.Body, "body", "", "Story body / description")
	cmd.Flags().StringVar(&req.AcceptanceCriteria, "ac", "", "Acceptance criteria")
	cmd.Flags().StringVar(&req.Status, "status", "", "Initial status (default: backlog)")
	cmd.Flags().StringVar(&req.Priority, "priority", "", "Priority (e.g. critical|high|medium|low)")
	cmd.Flags().StringVar(&req.Category, "category", "", "Category (e.g. bug|feature|improvement)")
	cmd.Flags().StringSliceVar(&req.Tags, "tag", nil, "Tag (repeat for multiple)")
	return cmd
}

func newStoryListCmd(configArg, userArg *string) *cobra.Command {
	var req verb.StoryListRequest
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List stories under a project",
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, err := resolveProjectID(req.ProjectID, *configArg, *userArg)
			if err != nil {
				return err
			}
			req.ProjectID = pid
			body, err := json.Marshal(req)
			if err != nil {
				return err
			}
			return runStoryVerb(cmd, "story_list", body, *configArg, *userArg)
		},
	}
	cmd.Flags().StringVar(&req.ProjectID, "project-id", "", "Project id (falls back to project_id in satellites.toml)")
	cmd.Flags().StringSliceVar(&req.Tags, "tag", nil, "Filter to stories carrying ALL listed tags (repeatable)")
	return cmd
}

func newStoryGetCmd(configArg, userArg *string) *cobra.Command {
	var req verb.StoryGetRequest
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Fetch a single story by id",
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(req.ID) == "" {
				return fmt.Errorf("--id required")
			}
			body, err := json.Marshal(req)
			if err != nil {
				return err
			}
			return runStoryVerb(cmd, "story_get", body, *configArg, *userArg)
		},
	}
	cmd.Flags().StringVar(&req.ID, "id", "", "Story id (required)")
	return cmd
}

func newStoryUpdateCmd(configArg, userArg *string) *cobra.Command {
	// Locals hold flag values; we copy to pointers only for the fields
	// the operator actually set, so omitted flags leave the stored
	// value untouched (StoryUpdateRequest uses pointer-or-nil semantics).
	var (
		id       string
		parentID string
		title    string
		body     string
		ac       string
		status   string
		priority string
		category string
		tags     []string
	)
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Patch mutable fields on a story",
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(id) == "" {
				return fmt.Errorf("--id required")
			}
			req := verb.StoryUpdateRequest{ID: id}
			if cmd.Flags().Changed("parent-id") {
				req.ParentID = &parentID
			}
			if cmd.Flags().Changed("title") {
				req.Title = &title
			}
			if cmd.Flags().Changed("body") {
				req.Body = &body
			}
			if cmd.Flags().Changed("ac") {
				req.AcceptanceCriteria = &ac
			}
			if cmd.Flags().Changed("status") {
				req.Status = &status
			}
			if cmd.Flags().Changed("priority") {
				req.Priority = &priority
			}
			if cmd.Flags().Changed("category") {
				req.Category = &category
			}
			if cmd.Flags().Changed("tag") {
				req.Tags = &tags
			}
			raw, err := json.Marshal(req)
			if err != nil {
				return err
			}
			return runStoryVerb(cmd, "story_update", raw, *configArg, *userArg)
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "Story id (required)")
	cmd.Flags().StringVar(&parentID, "parent-id", "", "Parent story id (empty string clears)")
	cmd.Flags().StringVar(&title, "title", "", "Title")
	cmd.Flags().StringVar(&body, "body", "", "Body")
	cmd.Flags().StringVar(&ac, "ac", "", "Acceptance criteria")
	cmd.Flags().StringVar(&status, "status", "", "Status")
	cmd.Flags().StringVar(&priority, "priority", "", "Priority")
	cmd.Flags().StringVar(&category, "category", "", "Category")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "Tag (repeat to replace the tag set)")
	return cmd
}

func runStoryVerb(cmd *cobra.Command, name string, body json.RawMessage, configArg, userArg string) error {
	resp, err := dispatchVerb(context.Background(), name, body, configArg, userArg)
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), string(resp))
	return nil
}
