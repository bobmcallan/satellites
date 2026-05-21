package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/spf13/cobra"
)

// stampCallerUser attaches a caller identity to ctx so verbs that
// fall back to auth.FromContext (workspace_create, project_create, …)
// stamp the right owner_user_id when invoked from the CLI.
//
// The CLI doesn't authenticate to a server — the operator declares
// who they are via flag/env. Empty userID is fine; verbs then
// produce NULL-owner rows as before. This is the bridge until the
// CLI-talks-to-server path lands.
func stampCallerUser(ctx context.Context, userID string) context.Context {
	if userID == "" {
		return ctx
	}
	return auth.WithUser(ctx, &auth.User{ID: userID})
}

// resolveCallerUserID returns the CLI's caller user id from the
// --user flag with $SATELLITES_USER_ID as fallback.
func resolveCallerUserID(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	return os.Getenv("SATELLITES_USER_ID")
}

func init() {
	var (
		jsonArg string
		userArg string
	)

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

			ctx := stampCallerUser(context.Background(), resolveCallerUserID(userArg))
			resp, err := verb.Dispatch(ctx, name, req)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(resp))
			return nil
		},
	}
	cmd.Flags().StringVar(&jsonArg, "json", "", "JSON request body (alternative to stdin)")
	cmd.Flags().StringVar(&userArg, "user", "", "Caller user id (overrides $SATELLITES_USER_ID). Stamped onto verbs that record owner_user_id.")
	register(cmd)
}
