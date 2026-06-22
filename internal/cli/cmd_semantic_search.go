package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bobmcallan/satellites/internal/cliconfig"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/spf13/cobra"
)

// `satellites semantic-search <query>` — query the workspace corpus by meaning
// (sty_e8db6032). Server-side verb: it needs the Gemini embedder + the Postgres
// chunk store, so it dispatches to the configured server (in-process only when
// no server is configured, where it returns an empty noted result).
func init() {
	var (
		configArg string
		userArg   string
		wsArg     string
		limitArg  int
		tagsArg   []string
	)
	cmd := &cobra.Command{
		Use:   "semantic-search <query>",
		Short: "Search the workspace document corpus by semantic similarity (server-side embeddings).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			wsID := strings.TrimSpace(wsArg)
			if wsID == "" {
				cfg, _, err := cliconfig.Load(configArg)
				if err != nil {
					return err
				}
				if strings.TrimSpace(cfg.ProjectID) == "" {
					return fmt.Errorf("no workspace: pass --workspace or configure project_id in satellites.toml")
				}
				ws, err := resolveWorkspaceID(ctx, cfg.ProjectID, configArg, userArg)
				if err != nil {
					return fmt.Errorf("resolve workspace_id: %w", err)
				}
				wsID = ws
			}

			req, err := json.Marshal(verb.SemanticSearchRequest{WorkspaceID: wsID, Query: args[0], Limit: limitArg, Tags: tagsArg})
			if err != nil {
				return err
			}
			raw, err := dispatchVerb(ctx, "semantic_search", req, configArg, userArg)
			if err != nil {
				return err
			}
			var resp verb.SemanticSearchResponse
			if err := json.Unmarshal(raw, &resp); err != nil {
				return err
			}

			w := cmd.OutOrStdout()
			if resp.Note != "" {
				fmt.Fprintf(w, "note: %s\n", resp.Note)
			}
			if len(resp.Results) == 0 {
				fmt.Fprintln(w, "no results")
				return nil
			}
			for _, r := range resp.Results {
				content := truncate(strings.ReplaceAll(r.Content, "\n", " "), 160)
				fmt.Fprintf(w, "[%.3f] %s (chunk %d)\n  %s\n", r.Score, r.DocumentName, r.ChunkIndex, content)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&configArg, "config", "", "Path to satellites.toml")
	cmd.Flags().StringVar(&userArg, "user", "", "Caller user id")
	cmd.Flags().StringVar(&wsArg, "workspace", "", "Workspace id to search (default: the configured project's workspace)")
	cmd.Flags().IntVar(&limitArg, "limit", 10, "Maximum number of results")
	cmd.Flags().StringSliceVar(&tagsArg, "tags", nil, "Narrow to documents carrying all tags (e.g. classification:security)")
	register(cmd)
}
