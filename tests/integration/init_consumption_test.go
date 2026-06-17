//go:build integration

package integration_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/cli"
)

// TestInitConfiguresConsumption pins the consumption contract (sty_985b05ec):
// `satellites init` onboards a repo by CONSUMPTION for skills/documents — it
// writes NO local .satellites/{skills,documents} and seeds the documented
// library_pins block in the toml. It DOES scaffold the order-zero substrate
// (the baseline workflow + a starter constitution under .satellites/principles;
// epic:satellites-backbone 2.4).
func TestInitConfiguresConsumption(t *testing.T) {
	repo := t.TempDir()
	tomlPath := filepath.Join(repo, ".satellites", "satellites.toml")

	// init resolves a fresh repo's root from the CWD (no toml exists yet to
	// resolve from). Chdir into the temp repo for the duration of the test.
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	runInit := func() (string, error) {
		root := cli.NewRootCmd()
		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetArgs([]string{"init"})
		err := root.Execute()
		return buf.String(), err
	}

	out, err := runInit()
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}

	// No local skills/documents scaffolded (those are CONSUMED), but the
	// ORDER-ZERO substrate IS scaffolded: the baseline workflow + a starter
	// constitution under .satellites/principles (epic:satellites-backbone 2.4).
	for _, sub := range []string{"skills", "documents"} {
		if _, statErr := os.Stat(filepath.Join(repo, ".satellites", sub)); !os.IsNotExist(statErr) {
			t.Errorf("init must not scaffold .satellites/%s (statErr=%v)", sub, statErr)
		}
	}
	if _, statErr := os.Stat(filepath.Join(repo, ".satellites", "principles", "constitution.md")); statErr != nil {
		t.Errorf("init must scaffold the starter constitution: %v", statErr)
	}

	// The toml carries the consumption knob.
	raw, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatalf("read toml: %v", err)
	}
	if !strings.Contains(string(raw), "library_pins") {
		t.Errorf("toml missing the library_pins consumption block:\n%s", raw)
	}

	// The report names consumption, not a scaffolded workflow.
	if !strings.Contains(out, "library_pins") && !strings.Contains(out, "CONSUME") {
		t.Errorf("init output should describe consumption, got:\n%s", out)
	}

	// Idempotent: a second run rewrites nothing.
	before := string(raw)
	if _, err := runInit(); err != nil {
		t.Fatalf("init re-run: %v", err)
	}
	after, _ := os.ReadFile(tomlPath)
	if string(after) != before {
		t.Errorf("init re-run rewrote the toml; must be idempotent")
	}
}
