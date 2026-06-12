// `satellites skill list` and `satellites skill get` — read-only
// access to the type:"skill" substrate rows. Thin shells over the
// document_list / document_get MCP verbs with a type:"skill" filter.
//
// No new MCP verbs introduced (project principle no-new-mcp-verbs).
// The noun lives in the CLI; the substrate keeps a single document_*
// surface.

package cli

import "github.com/spf13/cobra"

func init() {
	var (
		configArg string
		userArg   string
	)
	skill := &cobra.Command{
		Use:   "skill",
		Short: "Skill substrate operations (list / get)",
	}
	skill.PersistentFlags().StringVar(&configArg, "config", "", "Path to satellites.toml (overrides $SATELLITES_CONFIG / .satellites/satellites.toml walk-up).")
	skill.PersistentFlags().StringVar(&userArg, "user", "", "Caller user id (overrides $SATELLITES_USER_ID). Stamped onto verbs when dispatching in-process.")

	skill.AddCommand(newSubstrateListCmd(substrateNounConfig{
		Use:        "list",
		Short:      "List skills (document_list type:\"skill\")",
		FilterType: "skill",
		ConfigArg:  &configArg,
		UserArg:    &userArg,
	}))
	skill.AddCommand(newSubstrateGetCmd(substrateNounConfig{
		Use:        "get",
		Short:      "Print a skill body (document_get name=<name>)",
		FilterType: "skill",
		ConfigArg:  &configArg,
		UserArg:    &userArg,
	}))
	skill.AddCommand(newUploadCmd(uploadConfig{
		Kind:      "skills",
		ConfigArg: &configArg,
		UserArg:   &userArg,
	}))
	skill.AddCommand(newSkillPublishCmd(&configArg, &userArg))
	skill.AddCommand(newSkillSyncCmd(&configArg, &userArg))
	skill.AddCommand(newSkillIndexCmd(&configArg, &userArg))
	skill.AddCommand(newCorpusReviewCmd("skills", &configArg, &userArg))
	register(skill)
}
