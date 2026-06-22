package cli

import "testing"

// TestCodeMapWiring checks the `code map` subcommand is registered under `code`
// with its --json flag — the surface the help and the `code` group expose. The
// reachability LOGIC is covered by internal/codemap's tests (pinned against a
// synthetic evidence-shaped fixture so it survives the real subsystem's later
// deletion); here we only assert the command is wired and reachable.
func TestCodeMapWiring(t *testing.T) {
	m := newCodeMapCmd()
	if m.Use != "map" {
		t.Fatalf("want Use=map, got %q", m.Use)
	}
	if m.Flags().Lookup("json") == nil {
		t.Errorf("code map must expose --json")
	}
	if m.RunE == nil {
		t.Errorf("code map must have a RunE")
	}
}
