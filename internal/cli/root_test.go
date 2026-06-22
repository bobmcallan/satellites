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

// sty_dbc4e3ff: every noun command group accepts both the singular and the
// plural form, so `satellites skills sync` resolves to the same command as
// `satellites skill sync`. The alternate form is a cobra alias (not a
// duplicate command), so root help still lists each noun once.
func TestNounGroups_SingularPluralAliases(t *testing.T) {
	// canonical (the command's Name) → the alias the user may also type.
	want := map[string]string{
		"skill":     "skills",
		"story":     "stories",
		"project":   "projects",
		"document":  "documents",
		"principle": "principles",
		"workspace": "workspaces",
		"workflow":  "workflows",
		"task":      "tasks",
		"ledger":    "ledgers",
		"changelog": "changelogs",
		"hook":      "hooks",
	}
	root := NewRootCmd()
	for canonical, plural := range want {
		// The plural resolves to the very same command object as the canonical.
		cSing, _, err := root.Find([]string{canonical})
		if err != nil || cSing == nil || cSing.Name() != canonical {
			t.Fatalf("canonical %q did not resolve: cmd=%v err=%v", canonical, cSing, err)
		}
		cPlur, _, err := root.Find([]string{plural})
		if err != nil || cPlur == nil {
			t.Fatalf("plural %q did not resolve: err=%v", plural, err)
		}
		if cPlur != cSing {
			t.Errorf("plural %q resolved to a different command than %q", plural, canonical)
		}
		// The alias is carried on the command (so `--help` prints it) rather
		// than registered as a separate top-level command.
		if !cSing.HasAlias(plural) {
			t.Errorf("command %q missing alias %q", canonical, plural)
		}
		if cPlur.Name() != canonical {
			t.Errorf("plural %q surfaced as its own command %q (should be an alias)", plural, cPlur.Name())
		}
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
