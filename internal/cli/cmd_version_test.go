package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionCmd_PrintsBuildInfo(t *testing.T) {
	root := NewRootCmd()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetArgs([]string{"version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	s := out.String()
	if s == "" {
		t.Fatalf("version output empty")
	}
	for _, want := range []string{"satellites ", "commit ", "built "} {
		if !strings.Contains(s, want) {
			t.Fatalf("version output missing %q: %s", want, s)
		}
	}
}

// TestRootVersionFlag_MatchesSubcommand pins AC #3 of sty_2b9bc47e:
// `version`, `--version`, and `-v` must produce byte-identical stdout.
func TestRootVersionFlag_MatchesSubcommand(t *testing.T) {
	run := func(args ...string) string {
		root := NewRootCmd()
		out := &bytes.Buffer{}
		root.SetOut(out)
		root.SetErr(out)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("execute %v: %v", args, err)
		}
		return out.String()
	}

	sub := run("version")
	long := run("--version")
	short := run("-v")

	if sub == "" {
		t.Fatalf("version subcommand output empty")
	}
	if long != sub {
		t.Fatalf("--version output mismatch:\n  sub:  %q\n  flag: %q", sub, long)
	}
	if short != sub {
		t.Fatalf("-v output mismatch:\n  sub:   %q\n  short: %q", sub, short)
	}
}
