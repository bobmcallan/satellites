// `satellites principle list` and `satellites principle get` —
// read-only access to documents tagged `principles:*`. Thin shells
// over the document_list / document_get MCP verbs. No new MCP verbs.

package cli

import "github.com/spf13/cobra"

func init() {
	var (
		configArg string
		userArg   string
	)
	principle := &cobra.Command{
		Use:   "principle",
		Short: "Principle document operations (list / get)",
	}
	principle.PersistentFlags().StringVar(&configArg, "config", "", "Path to satellites.toml (overrides $SATELLITES_CONFIG / .satellites/satellites.toml walk-up).")
	principle.PersistentFlags().StringVar(&userArg, "user", "", "Caller user id (overrides $SATELLITES_USER_ID). Stamped onto verbs when dispatching in-process.")

	principle.AddCommand(newSubstrateListCmd(substrateNounConfig{
		Use:             "list",
		Short:           "List principles (document_list tags:[principles:<scope>])",
		FilterTagPrefix: "principles:",
		ConfigArg:       &configArg,
		UserArg:         &userArg,
	}))
	principle.AddCommand(newSubstrateGetCmd(substrateNounConfig{
		Use:             "get",
		Short:           "Print a principle body (document_get name=<name>)",
		FilterTagPrefix: "principles:",
		ConfigArg:       &configArg,
		UserArg:         &userArg,
	}))
	principle.AddCommand(newUploadCmd(uploadConfig{
		Kind:      "principles",
		ConfigArg: &configArg,
		UserArg:   &userArg,
	}))
	register(principle)
}
