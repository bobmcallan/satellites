package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bobmcallan/satellites/internal/synth"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/spf13/cobra"
)

// `satellites workspace objective <workspace-id>` synthesizes the workspace's
// objective from its corpus (sty_a0099c04). Two executors, "configuration over
// code": `--executor gemini` (default) dispatches the server verb (Gemini runs
// it server-side); `--executor claude` runs `claude -p` locally over the same
// task prompt and writes the objective back. claude -p never runs server-side.
func init() {
	var (
		configArg string
		userArg   string
	)
	ws := &cobra.Command{
		Use:   "workspace",
		Short: "Workspace commands (synthesize a workspace's objective from its corpus).",
	}
	ws.PersistentFlags().StringVar(&configArg, "config", "", "Path to satellites.toml")
	ws.PersistentFlags().StringVar(&userArg, "user", "", "Caller user id")
	ws.AddCommand(newWorkspaceObjectiveCmd(&configArg, &userArg))
	register(ws)
}

func newWorkspaceObjectiveCmd(configArg, userArg *string) *cobra.Command {
	var (
		executor  string
		claudeBin string
	)
	cmd := &cobra.Command{
		Use:   "objective <workspace-id>",
		Short: "Generate (or refresh) a workspace's objective from its document corpus.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			wsID := strings.TrimSpace(args[0])
			w := cmd.OutOrStdout()

			switch strings.ToLower(strings.TrimSpace(executor)) {
			case "", "gemini", "server":
				// Server executor: the verb runs Gemini server-side.
				req, _ := json.Marshal(verb.WorkspaceObjectiveGenerateRequest{WorkspaceID: wsID})
				raw, err := dispatchVerb(ctx, "workspace_objective_generate", req, *configArg, *userArg)
				if err != nil {
					return err
				}
				var resp verb.WorkspaceObjectiveGenerateResponse
				if err := json.Unmarshal(raw, &resp); err != nil {
					return err
				}
				if !resp.Generated {
					fmt.Fprintf(w, "not generated: %s\n", resp.Note)
					return nil
				}
				fmt.Fprintf(w, "objective (%s):\n\n%s\n", resp.DocumentID, resp.Objective)
				return nil

			case "claude", "local":
				// Client executor: run claude -p locally over the same task prompt.
				return runObjectiveClaude(ctx, wsID, claudeBin, *configArg, *userArg, w)

			default:
				return fmt.Errorf("unknown --executor %q (want gemini|claude)", executor)
			}
		},
	}
	cmd.Flags().StringVar(&executor, "executor", "gemini", "Generation executor: gemini (server-side) | claude (local claude -p)")
	cmd.Flags().StringVar(&claudeBin, "claude-bin", "", "Path to the claude binary (claude executor; default: claude on PATH)")
	return cmd
}

// runObjectiveClaude fetches the workspace corpus over the verb surface, builds
// the shared objective prompt, runs claude -p locally, and writes the objective
// back — the client-side executor (claude -p never runs server-side).
func runObjectiveClaude(ctx context.Context, wsID, claudeBin, configPath, userArg string, w interface{ Write([]byte) (int, error) }) error {
	listReq, _ := json.Marshal(verb.DocumentListRequest{Scope: "workspace", WorkspaceID: wsID, Type: "document", Limit: 200})
	listRaw, err := dispatchVerb(ctx, "document_list", listReq, configPath, userArg)
	if err != nil {
		return fmt.Errorf("list corpus: %w", err)
	}
	var list verb.DocumentListResponse
	if err := json.Unmarshal(listRaw, &list); err != nil {
		return err
	}
	var corpus []synth.CorpusDoc
	for _, d := range list.Items {
		if d.Name == synth.ObjectiveDocName {
			continue
		}
		getReq, _ := json.Marshal(verb.DocumentGetRequest{Scope: "workspace", WorkspaceID: wsID, Name: d.Name})
		getRaw, err := dispatchVerb(ctx, "document_get", getReq, configPath, userArg)
		if err != nil {
			continue
		}
		var got verb.DocumentGetResponse
		if err := json.Unmarshal(getRaw, &got); err != nil {
			continue
		}
		corpus = append(corpus, synth.CorpusDoc{Name: d.Name, Body: got.RawBody})
	}
	if len(corpus) == 0 {
		fmt.Fprintln(w, "not generated: no corpus documents to synthesize from")
		return nil
	}

	gen := synth.ClaudeLocalGenerator{BinaryPath: strings.TrimSpace(claudeBin), Model: reviewerModel(configPath)}
	text, err := gen.Generate(ctx, synth.BuildObjectivePrompt(corpus))
	if err != nil {
		return err
	}

	upReq, _ := json.Marshal(verb.DocumentUpsertRequest{
		Scope: "workspace", WorkspaceID: wsID, Name: synth.ObjectiveDocName, Type: "document", Body: text,
	})
	if _, err := dispatchVerb(ctx, "document_upsert", upReq, configPath, userArg); err != nil {
		return fmt.Errorf("store objective: %w", err)
	}
	fmt.Fprintf(w, "objective (claude -p):\n\n%s\n", text)
	return nil
}
