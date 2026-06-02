// `satellites deploy` — the substrate↔local reconcile (sty_be65b4dd).
//
// One deterministic verb for the operator-refresh cycle that used to be a
// prose `satellites-init` routine: push the committed config/ sources up,
// then pull .claude/skills/ back into agreement. Per
// document:project/client-command-surface it owns config↔substrate sync.
//
// Pure composition of already-factored logic — no new MCP verb
// (document:project/no-new-mcp-verbs):
//
//  1. validateUpload — the whole config/ tree against the inclusion rule
//     (aborts before any dispatch, naming file + rule).
//  2. uploadKind × {documents, skills, principles} — push.
//  3. syncSkills (project scope) — reconcile .claude/skills by identity
//     stamp: install / update / remove / refuse-conflict / never-touch.

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
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
		Short: "Reconcile substrate ↔ local: validate + upload config/ sources, then sync .claude/skills",
		Long: `deploy is the substrate↔local reconcile. It validates the whole config/
source tree (aborting on any violation), uploads documents + skills +
principles, then reconciles .claude/skills/ against the substrate
(install/update/remove by identity stamp; never clobbering a locally-edited
or operator-authored skill). Composes existing verbs — no new MCP surface.`,
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
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Validate + print the planned upserts and reconcile actions without writing.")
	register(cmd)
}

func runDeploy(ctx context.Context, out io.Writer, configArg, userArg string, dryRun bool) error {
	// 1. Validate the whole source tree once; abort before any dispatch.
	violations, err := validateUpload(configRoot, filepath.Join(".claude", "skills"))
	if err != nil {
		return err
	}
	if len(violations) > 0 {
		fmt.Fprintf(out, "validation failed — %d violation(s), nothing deployed:\n", len(violations))
		for _, v := range violations {
			fmt.Fprintf(out, "  ✗ %s\n", v.String())
		}
		return fmt.Errorf("deploy refused: %d validation violation(s)", len(violations))
	}

	// 2. Push: upload every kind (single source: uploadKind).
	fmt.Fprintln(out, "── push: config/ → substrate ──")
	for _, kind := range []string{"documents", "skills", "principles"} {
		if err := uploadKind(ctx, out, kind, configArg, userArg, dryRun); err != nil {
			return err
		}
	}

	// 3. Pull: reconcile .claude/skills against the repo's project (single
	// source: syncSkills).
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
