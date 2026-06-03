// `satellites deploy` — pull the substrate's skills into local agreement
// (sty_be65b4dd).
//
// deploy is PULL-ONLY: it reconciles .claude/skills/ against this repo's
// project scope by identity stamp. The push half (.satellites/ → substrate via
// document/skill/principle upload) is a separate client verb the agent
// invokes deliberately as a prompt — it is NOT coupled into deploy, so an
// agent session that runs deploy never uploads anything (operator decision
// on the sty_be65b4dd path/push report).
//
// Pure composition of already-factored logic — no new MCP verb
// (document:project/no-new-mcp-verbs): syncSkills (project scope) reconciles
// .claude/skills by identity stamp — install / update / remove /
// refuse-conflict / never-touch.

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/bobmcallan/satellites/internal/cliconfig"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/spf13/cobra"
)

func init() {
	var (
		configArg string
		userArg   string
		dryRun    bool
	)
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Pull the substrate's project skills into .claude/skills (stamp-reconciled, pull-only)",
		Long: `deploy reconciles .claude/skills/ against this repo's project scope in
the substrate (install/update/remove by identity stamp; never clobbering a
locally-edited or operator-authored skill). It is pull-only: pushing .satellites/
sources up is a separate client verb (document/skill/principle upload) invoked
deliberately, not coupled into deploy. Composes existing verbs — no new MCP
surface.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return runDeploy(ctx, cmd.OutOrStdout(), configArg, userArg, dryRun)
		},
	}
	cmd.Flags().StringVar(&configArg, "config", "", "Path to satellites.toml (overrides $SATELLITES_CONFIG / .satellites/satellites.toml walk-up).")
	cmd.Flags().StringVar(&userArg, "user", "", "Caller user id (overrides $SATELLITES_USER_ID).")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print the reconcile actions without writing files.")
	register(cmd)
}

func runDeploy(ctx context.Context, out io.Writer, configArg, userArg string, dryRun bool) error {
	// Pull: reconcile .claude/skills against the repo's project (single
	// source: syncSkills). skillsRoot is anchored to the repo dir, not CWD.
	fmt.Fprintln(out, "── pull: substrate → .claude/skills ──")
	ws, pj, err := resolveDeployScope(ctx, configArg, userArg)
	if err != nil {
		return err
	}
	return syncSkills(ctx, out, "project", ws, pj, configArg, userArg, "", dryRun)
}

// resolveDeployScope reads project_id from the repo config and resolves
// its workspace_id via project_get, so the pull reconciles this repo's
// project scope.
func resolveDeployScope(ctx context.Context, configArg, userArg string) (ws, pj string, err error) {
	cfg, _, lerr := cliconfig.Load(configArg)
	if lerr != nil && !errors.Is(lerr, cliconfig.ErrNotFound) {
		return "", "", lerr
	}
	pj = strings.TrimSpace(cfg.ProjectID)
	if pj == "" {
		return "", "", fmt.Errorf("deploy: no project_id in config — set it or run `satellites project match`")
	}
	req, err := json.Marshal(verb.ProjectGetRequest{ID: pj})
	if err != nil {
		return "", "", err
	}
	raw, err := dispatchVerb(ctx, "project_get", req, configArg, userArg)
	if err != nil {
		return "", "", fmt.Errorf("deploy: resolve workspace for project %s: %w", pj, err)
	}
	// project_get keeps the base Project fields top-level on the wire;
	// we only need workspace_id.
	var resp struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", "", err
	}
	ws = strings.TrimSpace(resp.WorkspaceID)
	if ws == "" {
		return "", "", fmt.Errorf("deploy: project %s has no workspace_id", pj)
	}
	return ws, pj, nil
}
