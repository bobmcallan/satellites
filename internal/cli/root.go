// Package cli wires the satellites V5 command-line interface.
//
// Subcommands self-register via init() calling register(), so adding a new
// verb is one new file under internal/cli/ — no edits to a central switch.
package cli

import "github.com/spf13/cobra"

var registered []*cobra.Command

// register attaches a subcommand to the root. Called from each subcommand
// file's init().
func register(c *cobra.Command) {
	registered = append(registered, c)
}

// NewRootCmd returns the root cobra command with every registered
// subcommand attached.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "satellites",
		Short: "Satellites V5 — authored-process substrate CLI",
		Long: `satellites attaches to whatever primary interface you use (Claude Code,
Warp, Codex, Gemini CLI) and governs the authored process for
agent-driven work. See https://github.com/bobmcallan/satellites for
docs.`,
		Version:       versionLine(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetVersionTemplate("{{.Version}}\n")
	for _, c := range registered {
		root.AddCommand(c)
	}
	return root
}
