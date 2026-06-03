package cli

import (
	"bytes"
	"strings"
	"testing"
)

// sty_76c6612d AC1+AC2: an unknown subcommand responds with the
// nearest-match suggestion (cobra) AND the usage block, and the error
// propagates so the process exits non-zero.
func TestUnknownCommand_SuggestsAndShowsUsage(t *testing.T) {
	root := NewRootCmd()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs([]string{"updat"}) // one edit from "update"

	err := root.Execute()
	if err == nil {
		t.Fatal("unknown command did not error (would exit zero)")
	}
	if !isUnknownCommandErr(err) {
		t.Fatalf("error not recognised as unknown-command: %v", err)
	}

	// Nearest-match suggestion is carried in the error itself (cobra).
	if !strings.Contains(strings.ToLower(err.Error()), "did you mean") {
		t.Errorf("no nearest-match suggestion in error: %v", err)
	}
	if !strings.Contains(err.Error(), "update") {
		t.Errorf("suggestion 'update' missing from error: %v", err)
	}

	// Usage block is what the Execute() wrapper appends on this path.
	printUnknownCommandHelp(out, root)
	s := out.String()
	if !strings.Contains(s, "Available Commands:") {
		t.Errorf("usage block (Available Commands) not shown: %s", s)
	}
}

// AC2: a far-off unknown command (no close match) still errors non-zero
// and still shows usage, even without a suggestion.
func TestUnknownCommand_NoCloseMatchStillErrors(t *testing.T) {
	root := NewRootCmd()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs([]string{"frobnicate"})

	err := root.Execute()
	if err == nil {
		t.Fatal("unknown command did not error")
	}
	if !isUnknownCommandErr(err) {
		t.Fatalf("error not recognised as unknown-command: %v", err)
	}
}

// AC3: a known command is unaffected — it neither errors as unknown nor
// gets a usage dump appended.
func TestKnownCommand_Unaffected(t *testing.T) {
	root := NewRootCmd()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs([]string{"version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("known command errored: %v", err)
	}
}
