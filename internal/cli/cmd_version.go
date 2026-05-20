package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Build info — overridden via -ldflags at release time
// (see sty_d3270775, the release workflow).
var (
	Version   = "dev"
	Commit    = "none"
	BuildTime = "unknown"
)

func init() {
	register(&cobra.Command{
		Use:   "version",
		Short: "Print build info",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintf(cmd.OutOrStdout(),
				"satellites %s (commit %s, built %s)\n",
				Version, Commit, BuildTime)
		},
	})
}
