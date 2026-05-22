// Project subcommands wrap the project_* verbs. Currently exposes
// `project match` — the bootstrap surface an operator/agent uses to
// resolve a project_id from any-form git remote URL after install,
// then persist that id into the TOML.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/spf13/cobra"
)

func init() {
	var (
		configArg string
		userArg   string
	)

	project := &cobra.Command{
		Use:   "project",
		Short: "Project commands (match a git remote to a project_id, …)",
	}
	project.PersistentFlags().StringVar(&configArg, "config", "", "Path to satellites.toml (overrides $SATELLITES_CONFIG / .satellites/satellites.toml walk-up).")
	project.PersistentFlags().StringVar(&userArg, "user", "", "Caller user id (overrides $SATELLITES_USER_ID). Stamped onto verbs when dispatching in-process.")

	project.AddCommand(newProjectMatchCmd(&configArg, &userArg))

	register(project)
}

func newProjectMatchCmd(configArg, userArg *string) *cobra.Command {
	var remote string
	cmd := &cobra.Command{
		Use:   "match",
		Short: "Resolve a project_id from a git remote URL",
		Long: `match canonicalises any-form git remote URL and returns the project
whose stored remote matches. Use the output to populate project_id
in .satellites/satellites.toml.

Accepted forms:
  https://github.com/owner/repo[.git]
  git@github.com:owner/repo.git
  ssh://git@github.com/owner/repo
  <scope>:github.com/owner/repo
  github.com/owner/repo`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(remote) == "" {
				return fmt.Errorf("--remote required")
			}
			req := verb.ProjectMatchRequest{GitURL: remote}
			raw, err := json.Marshal(req)
			if err != nil {
				return err
			}
			resp, err := dispatchVerb(context.Background(), "project_match", raw, *configArg, *userArg)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(resp))
			return nil
		},
	}
	cmd.Flags().StringVar(&remote, "remote", "", "Git remote URL (any supported form; required)")
	return cmd
}
