package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/bobmcallan/satellites/internal/auth"
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

// projectScopedExecVerbs are the verbs whose project_id is a SCOPING filter (not
// an id): omitting it fans out across EVERY project the caller can see — the
// 2026-06-22 cross-project leak (sty_8473bba2), worst as admin. For these, a
// repo-scoped exec defaults project_id to the satellites.toml project when the
// caller omits it, so an unscoped query run from a repo cannot reach other
// projects by accident.
var projectScopedExecVerbs = map[string]bool{
	"document_list":   true,
	"document_count":  true,
	"semantic_search": true,
	"document_upsert": true,
}

// confineToConfigProject defaults a project-scoped verb's project_id to the
// repo's config project when the caller omitted it. An explicit project_id is
// honoured unchanged (admin can still target another project deliberately);
// outside a repo / with no config project, nothing changes. This is the
// belt-and-braces guard behind the per-skill fix: confinement is the default,
// not something each author must remember.
func confineToConfigProject(name string, req json.RawMessage, configArg string) json.RawMessage {
	if !projectScopedExecVerbs[name] {
		return req
	}
	m := map[string]json.RawMessage{}
	if len(req) > 0 {
		if err := json.Unmarshal(req, &m); err != nil {
			return req // not a JSON object — leave it untouched
		}
	}
	if v, ok := m["project_id"]; ok {
		var s string
		if json.Unmarshal(v, &s) == nil && s != "" {
			return req // explicit project_id — honour it
		}
	}
	pid, err := projectIDFromConfig(configArg)
	if err != nil || pid == "" {
		return req // not in a repo / no config project — unchanged
	}
	pidRaw, mErr := json.Marshal(pid)
	if mErr != nil {
		return req
	}
	m["project_id"] = pidRaw
	out, mErr := json.Marshal(m)
	if mErr != nil {
		return req
	}
	return out
}

func init() {
	var (
		jsonArg   string
		userArg   string
		configArg string
	)

	cmd := &cobra.Command{
		Use:   "exec <verb>",
		Short: "Dispatch a verb with JSON args (single-execution-path entry)",
		Long: `exec dispatches a verb. When a satellites-server is configured
(.satellites/satellites.toml with server_url, plus an api-key in the
credential store from 'satellites auth'), the verb is posted to POST
/api/v1/exec/<name> on that server. When no config is found, the verb is
dispatched in-process against the local registry.

The api-key resolves from the credential store ('satellites auth'), OR from
the SATELLITES_API_KEY environment variable for headless/CI use. When both are
present the environment variable wins; when neither is, exec reports a clear
no-api-key error naming both paths.`,
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

			// Confine a project-scoped verb to the repo's config project when the
			// caller didn't scope it (sty_8473bba2) — no accidental cross-project
			// fan-out, even as admin.
			req = confineToConfigProject(name, req, configArg)

			resp, err := dispatchVerb(context.Background(), name, req, configArg, userArg)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(resp))
			return nil
		},
	}
	cmd.Flags().StringVar(&jsonArg, "json", "", "JSON request body (alternative to stdin)")
	cmd.Flags().StringVar(&userArg, "user", "", "Caller user id (overrides $SATELLITES_USER_ID). Stamped onto verbs that record owner_user_id when dispatching in-process.")
	cmd.Flags().StringVar(&configArg, "config", "", "Path to satellites.toml (overrides $SATELLITES_CONFIG / .satellites/satellites.toml walk-up).")
	register(cmd)
}
