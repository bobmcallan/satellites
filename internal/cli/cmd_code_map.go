// `satellites code map` — the durable reachability / orphan map (sty_8834343c,
// epic:code-reachability). It re-runs the one-shot reachability report
// (docs/reports/reachability-2026-06-23.md) as a command: it derives every entry
// point across the four surfaces from their REGISTRIES — the cobra command tree,
// the MCP verb catalog (internal/verb) + the advertised set
// (mcpserver.ExposedVerbs), the server mux (internal/server/server.go), and the
// harness hooks (.claude/settings.json) — joins each to its system INVOKERS, and
// flags ORPHANS (no invoker, data dead-ends) so dead-feature drift (the
// `evidence` shape) is caught continuously, not by a manual trace.
//
// No command/route/verb NAME is hardcoded (epic:enforcement-surface): the
// surfaces come from the live registries. Handler symbols resolve via the code
// index (reused, not re-walked — `code index` must have run).
package cli

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/bobmcallan/satellites/internal/codeindex"
	"github.com/bobmcallan/satellites/internal/codemap"
	"github.com/spf13/cobra"
)

func newCodeMapCmd() *cobra.Command {
	var mapConfig string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "map",
		Short: "Reachability/orphan map: entry points → invokers → data, across CLI/server/MCP/hook",
		Long: `map derives every entry point across the four surfaces from their
registries (cobra tree, MCP verb catalog, server mux, settings.json hooks),
joins each to its system invokers, and flags orphans — entry points with no
system invoker whose data dead-ends in other un-invoked paths (the dead-feature
shape generic deadcode/unused miss). Reuses the code index for symbol
resolution; run ` + "`code index`" + ` first.

--json emits the stable machine form:
  {
    "repo_root": "<abs path>",
    "entries": [ {
        "surface":  "cli|server|mcp|hook",
        "name":     "<command path | route | verb | hook event>",
        "handler":  "<symbol>", "file": "<repo-rel>", "line": <int>,
        "exposed":  <bool>,                 // mcp: advertised as a tool
        "invokers": [ {"source":"settings.json|skill|github|script|build|go|http","file":..,"line":..} ],
        "produces": ["<ledger kind>"], "consumes": ["<ledger kind>"],
        "orphan":   <bool>,
        "confidence":"high|medium|low",     // orphans only
        "note":     "<why>"
    } ],
    "orphans": [ <the orphan subset, highest confidence first> ]
  }
An orphan has no system invoker; confidence HIGH means it produces a ledger kind
read only by other un-invoked paths (a dead producer→consumer cluster).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, dbPath := resolveCodeIndex(mapConfig)
			return runCodeMap(cmd.OutOrStdout(), cmd.Root(), repoRoot, dbPath, asJSON)
		},
	}
	cmd.Flags().StringVar(&mapConfig, "config", "", "Path to satellites.toml (resolves repo root; defaults to walk-up from CWD).")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Emit the map as JSON (stable machine form) instead of the human table.")
	return cmd
}

func runCodeMap(out io.Writer, root *cobra.Command, repoRoot, dbPath string, asJSON bool) error {
	store, err := codeindex.Open(dbPath)
	if err != nil {
		return fmt.Errorf("code map: open index: %w", err)
	}
	defer store.Close()

	resolve := func(short string) (string, int, bool) {
		if short == "" {
			return "", 0, false
		}
		syms, err := store.ByName(short)
		if err != nil || len(syms) == 0 {
			return "", 0, false
		}
		return syms[0].File, syms[0].StartLine, true
	}

	entries := codemap.WalkCLI(root, repoRoot, resolve)
	entries = append(entries, codemap.CollectVerbEntries(resolve)...)

	hookEntries, hookInvoked := codemap.ParseHooks(filepath.Join(repoRoot, ".claude", "settings.json"))
	entries = append(entries, hookEntries...)
	entries = append(entries, codemap.ParseRoutes(filepath.Join(repoRoot, "internal", "server", "server.go"), resolve)...)

	consumed := codemap.ScanDataEdges(repoRoot, entries)
	codemap.ScanInvokers(repoRoot, entries, hookInvoked)

	m := &codemap.Map{RepoRoot: repoRoot, Entries: entries, ConsumedKinds: consumed}
	m.SortEntries()
	m.Classify()

	if asJSON {
		return m.RenderJSON(out)
	}
	m.Render(out)
	return nil
}
