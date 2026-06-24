// `satellites story` — story commands. The one execution primitive is
// `story review` (the reviewer gate). The former `story run` executor driver
// (sty_7af47a91) was retired in sty_6e1c3641: under the reviewer-only model the
// local agent is the executor, so satellites does not spawn `claude -p` to do
// the work — it only gates transitions. `review` is that one config-driven
// `claude -p` path; the reviewer is the gate skill it runs.

package cli

import "github.com/spf13/cobra"

func init() {
	var (
		configArg string
		userArg   string
	)

	story := &cobra.Command{
		Use:     "story",
		Aliases: []string{"stories"},
		Short:   "Story commands (gate a story's transitions via claude -p)",
	}
	story.PersistentFlags().StringVar(&configArg, "config", "", "Path to satellites.toml (overrides $SATELLITES_CONFIG / .satellites/satellites.toml walk-up).")
	story.PersistentFlags().StringVar(&userArg, "user", "", "Caller user id (overrides $SATELLITES_USER_ID). Stamped onto verbs when dispatching in-process.")

	story.AddCommand(newStoryCreateCmd(&configArg, &userArg))
	story.AddCommand(newStoryReviewCmd(&configArg, &userArg))
	story.AddCommand(newStorySetStatusCmd(&configArg, &userArg))
	story.AddCommand(newStoryGetCmd(&configArg, &userArg))
	story.AddCommand(newStoryChildrenCmd(&configArg, &userArg))
	story.AddCommand(newStoryValidateCmd(&configArg, &userArg))
	story.AddCommand(newStoryOutputCmd(&configArg, &userArg))

	register(story)
}
