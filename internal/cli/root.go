// Package cli wires the satellites V5 command-line interface.
//
// Subcommands self-register via init() calling register(), so adding a new
// verb is one new file under internal/cli/ — no edits to a central switch.
package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

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

// Execute runs the root command and, on an unknown subcommand, follows
// the bare cobra error with the command's usage so a mistyped command
// guides the user instead of dead-ending (sty_76c6612d). Cobra already
// embeds a "Did you mean this?" nearest-match in the error for close
// typos; this adds the usage block on top. Normal RunE errors keep their
// quiet, usage-free output (root has SilenceUsage/SilenceErrors set).
// Returns the command's error so the caller sets a non-zero exit.
func Execute() error {
	root := NewRootCmd()
	err := root.Execute()
	if err != nil {
		fmt.Fprintln(root.ErrOrStderr(), err)
		if isUnknownCommandErr(err) {
			printUnknownCommandHelp(root.ErrOrStderr(), root)
		}
	}
	return err
}

// isUnknownCommandErr reports whether err is cobra's "unknown command"
// error. Cobra exposes no typed error for this, but the message prefix
// (`unknown command %q for %q`) is stable across releases.
func isUnknownCommandErr(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "unknown command")
}

// printUnknownCommandHelp writes the root usage block so an unknown
// command response names the available commands.
func printUnknownCommandHelp(w io.Writer, root *cobra.Command) {
	fmt.Fprintln(w)
	fmt.Fprint(w, root.UsageString())
}
