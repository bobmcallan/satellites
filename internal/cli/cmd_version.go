package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/spf13/cobra"
)

func init() {
	register(&cobra.Command{
		Use:   "version",
		Short: "Print build info",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := verb.Dispatch(context.Background(), "version", nil)
			if err != nil {
				return err
			}
			var info verb.VersionInfo
			if err := json.Unmarshal(resp, &info); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"satellites %s (commit %s, built %s)\n",
				info.Version, info.Commit, info.BuildTime)
			return nil
		},
	})
}
