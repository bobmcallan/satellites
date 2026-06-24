// `satellites code` — the in-client code symbol index (sty_08b82e4a,
// epic:code-index). It lets the agent search symbols and fetch exact source
// slices instead of reading whole files, the same token win an external
// code-index MCP gives but as a first-party CLI the agent drives over Bash.
//
//	code index           build/refresh .satellites/index.db for the repo
//	code search <query>  list matching symbols (name, kind, file:line)
//	code symbol <name>   print the exact source slice for a symbol
//
// CLI-only, NO MCP: these are commands run via Bash, reading/writing the
// per-repo index. The index store + schema are language-neutral; symbols are
// extracted by language — Go via go/ast, every other language via the CGo-free
// pure-Go gotreesitter runtime (sty_380a116d). `code index` is incremental:
// only files whose content changed since the last run are re-parsed. See
// internal/codeindex.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/bobmcallan/satellites/internal/cliconfig"
	"github.com/bobmcallan/satellites/internal/codeindex"
	"github.com/spf13/cobra"
)

func init() {
	code := &cobra.Command{
		Use:   "code",
		Short: "In-client code symbol index — search symbols + fetch exact source slices",
		Long: `code is a first-party, repo-agnostic symbol index the agent searches
instead of reading whole files. It builds a per-repo .satellites/index.db
(pure-Go SQLite + FTS5) and answers symbol queries and exact source-slice
fetches over Bash — no MCP server. Run ` + "`code index`" + ` once per session (or
after large changes), then ` + "`code search`" + ` / ` + "`code symbol`" + ` to navigate.`,
	}

	var indexConfig string
	var indexFull bool
	indexCmd := &cobra.Command{
		Use:   "index",
		Short: "Build or refresh the repo symbol index (.satellites/index.db)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, dbPath := resolveCodeIndex(indexConfig)
			return runCodeIndex(cmd.OutOrStdout(), repoRoot, dbPath, indexFull)
		},
	}
	indexCmd.Flags().StringVar(&indexConfig, "config", "", "Path to satellites.toml (resolves repo root; defaults to walk-up from CWD).")
	indexCmd.Flags().BoolVar(&indexFull, "full", false, "Re-parse every file, ignoring the incremental content-hash cache.")
	code.AddCommand(indexCmd)

	var searchConfig string
	var searchLimit int
	var searchJSON bool
	searchCmd := &cobra.Command{
		Use:   "search <query>",
		Short: "List indexed symbols matching a query (name, kind, file:line)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, dbPath := resolveCodeIndex(searchConfig)
			return runCodeSearch(cmd.OutOrStdout(), dbPath, args[0], searchLimit, searchJSON)
		},
	}
	searchCmd.Flags().StringVar(&searchConfig, "config", "", "Path to satellites.toml (resolves repo root; defaults to walk-up from CWD).")
	searchCmd.Flags().IntVar(&searchLimit, "limit", 25, "Max results (0 = no limit).")
	searchCmd.Flags().BoolVar(&searchJSON, "json", false, "Emit matching symbols as a JSON array (name, kind, signature, file, start_line, end_line).")
	code.AddCommand(searchCmd)

	var symbolsConfig string
	var symbolsJSON bool
	symbolsCmd := &cobra.Command{
		Use:   "symbols",
		Short: "List every indexed symbol (the raw symbol feed; pair with --json for graph construction)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, dbPath := resolveCodeIndex(symbolsConfig)
			return runCodeSymbols(cmd.OutOrStdout(), dbPath, symbolsJSON)
		},
	}
	symbolsCmd.Flags().StringVar(&symbolsConfig, "config", "", "Path to satellites.toml (resolves repo root; defaults to walk-up from CWD).")
	symbolsCmd.Flags().BoolVar(&symbolsJSON, "json", false, "Emit every indexed symbol as a JSON array (name, kind, signature, file, start_line, end_line).")
	code.AddCommand(symbolsCmd)

	var symbolConfig string
	symbolCmd := &cobra.Command{
		Use:   "symbol <name>",
		Short: "Print the exact source slice for a symbol by name",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, dbPath := resolveCodeIndex(symbolConfig)
			return runCodeSymbol(cmd.OutOrStdout(), repoRoot, dbPath, args[0])
		},
	}
	symbolCmd.Flags().StringVar(&symbolConfig, "config", "", "Path to satellites.toml (resolves repo root; defaults to walk-up from CWD).")
	code.AddCommand(symbolCmd)

	code.AddCommand(newCodeMapCmd())
	code.AddCommand(newCodeGraphCmd())

	register(code)
}

// resolveCodeIndex returns the repo root (walked for indexing, joined for source
// slices) and the index.db path under the shared data dir (<data_dir>/index.db,
// default <repo>/.satellites/index.db). Mirrors resolveStateDB: an unconfigured
// repo falls back to a CWD-rooted default, and a data_dir override relocates the
// index alongside state.db.
func resolveCodeIndex(configArg string) (repoRoot, dbPath string) {
	cfg, path, err := cliconfig.Load(configArg)
	if err != nil || strings.TrimSpace(path) == "" {
		repoRoot = cwdOrDot()
	} else {
		repoRoot = cliconfig.RepoRootFromConfigPath(path)
	}
	return repoRoot, cfg.ResolveIndexDB(repoRoot)
}

func runCodeIndex(out io.Writer, repoRoot, dbPath string, full bool) error {
	store, err := codeindex.Open(dbPath)
	if err != nil {
		return fmt.Errorf("code index: %w", err)
	}
	defer store.Close()
	stats, err := codeindex.IndexRepo(store, repoRoot, full)
	if err != nil {
		return fmt.Errorf("code index: %w", err)
	}
	fmt.Fprintf(out, "indexed %d symbols (%d file(s) parsed, %d unchanged) → %s\n",
		stats.TotalSymbols, stats.FilesParsed, stats.Unchanged, dbPath)
	if stats.Deleted > 0 {
		fmt.Fprintf(out, "(%d deleted file(s) pruned)\n", stats.Deleted)
	}
	if stats.ParseErrors > 0 {
		fmt.Fprintf(out, "(%d file(s) skipped on parse error)\n", stats.ParseErrors)
	}
	return nil
}

func runCodeSearch(out io.Writer, dbPath, query string, limit int, asJSON bool) error {
	store, err := codeindex.Open(dbPath)
	if err != nil {
		return fmt.Errorf("code search: %w", err)
	}
	defer store.Close()
	syms, err := store.Search(query, limit)
	if err != nil {
		return fmt.Errorf("code search: %w", err)
	}
	if asJSON {
		return writeSymbolsJSON(out, syms)
	}
	if len(syms) == 0 {
		fmt.Fprintf(out, "no symbols match %q (have you run `satellites code index`?)\n", query)
		return nil
	}
	for _, s := range syms {
		fmt.Fprintf(out, "%-8s %s\t%s:%d\n", s.Kind, s.Signature, s.File, s.StartLine)
	}
	return nil
}

// runCodeSymbols lists every indexed symbol. It is the raw symbol feed an agent
// reads to construct a richer artifact (e.g. a codegraph): with --json it pairs
// with `code graph --json` (the package spine) as the per-symbol fill, sourced
// from index.db with no re-parse. A missing/empty index yields an empty list
// (Open creates an empty db), not an error.
func runCodeSymbols(out io.Writer, dbPath string, asJSON bool) error {
	store, err := codeindex.Open(dbPath)
	if err != nil {
		return fmt.Errorf("code symbols: %w", err)
	}
	defer store.Close()
	syms, err := store.All()
	if err != nil {
		return fmt.Errorf("code symbols: %w", err)
	}
	if asJSON {
		return writeSymbolsJSON(out, syms)
	}
	if len(syms) == 0 {
		fmt.Fprintf(out, "no symbols indexed (have you run `satellites code index`?)\n")
		return nil
	}
	for _, s := range syms {
		fmt.Fprintf(out, "%-8s %s\t%s:%d\n", s.Kind, s.Signature, s.File, s.StartLine)
	}
	return nil
}

// codeSymbolJSON is the stable JSON shape for a symbol — the raw index fields
// only. No package or visibility field is invented: a consumer derives package
// from `file` and public-ness from `signature`.
type codeSymbolJSON struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Signature string `json:"signature"`
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

// writeSymbolsJSON emits symbols as an indented JSON array. An empty result
// marshals as `[]` (never `null`), so a missing/empty index is a clean empty
// feed rather than an error.
func writeSymbolsJSON(out io.Writer, syms []codeindex.Symbol) error {
	rows := make([]codeSymbolJSON, 0, len(syms))
	for _, s := range syms {
		rows = append(rows, codeSymbolJSON{
			Name:      s.Name,
			Kind:      s.Kind,
			Signature: s.Signature,
			File:      s.File,
			StartLine: s.StartLine,
			EndLine:   s.EndLine,
		})
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rows); err != nil {
		return fmt.Errorf("code symbols: encode json: %w", err)
	}
	return nil
}

func runCodeSymbol(out io.Writer, repoRoot, dbPath, name string) error {
	store, err := codeindex.Open(dbPath)
	if err != nil {
		return fmt.Errorf("code symbol: %w", err)
	}
	defer store.Close()
	syms, err := store.ByName(name)
	if err != nil {
		return fmt.Errorf("code symbol: %w", err)
	}
	if len(syms) == 0 {
		fmt.Fprintf(out, "no symbol named %q (try `satellites code search %s`)\n", name, name)
		return nil
	}
	for i, s := range syms {
		if i > 0 {
			fmt.Fprintln(out)
		}
		fmt.Fprintf(out, "// %s:%d-%d (%s)\n", s.File, s.StartLine, s.EndLine, s.Kind)
		slice, err := codeindex.ReadSlice(repoRoot, s)
		if err != nil {
			fmt.Fprintf(out, "%v\n", err)
			continue
		}
		out.Write(slice)
		fmt.Fprintln(out)
	}
	return nil
}
