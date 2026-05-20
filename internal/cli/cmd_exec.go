package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/spf13/cobra"
)

func init() {
	var jsonArg string

	cmd := &cobra.Command{
		Use:   "exec <verb>",
		Short: "Dispatch a verb with JSON args (single-execution-path entry)",
		Long: `exec is the verb-name entry point. The MCP transport dispatches
identically — same code path, same response shape. Both CLI and MCP
callers ultimately hit verb.Dispatch.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			var req json.RawMessage

			switch {
			case jsonArg != "":
				req = json.RawMessage(jsonArg)
			default:
				// Read stdin if piped, otherwise leave req nil.
				if stat, err := os.Stdin.Stat(); err == nil && (stat.Mode()&os.ModeCharDevice) == 0 {
					b, err := io.ReadAll(cmd.InOrStdin())
					if err != nil {
						return fmt.Errorf("read stdin: %w", err)
					}
					if len(b) > 0 {
						req = json.RawMessage(b)
					}
				}
			}

			resp, err := verb.Dispatch(context.Background(), name, req)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(resp))
			return nil
		},
	}
	cmd.Flags().StringVar(&jsonArg, "json", "", "JSON request body (alternative to stdin)")
	register(cmd)
}
