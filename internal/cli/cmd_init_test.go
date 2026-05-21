package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInit_WritesConfig(t *testing.T) {
	dir := t.TempDir()

	if err := WriteConfig(dir, Config{ServerURL: "https://example.com", APIKey: "k1"}); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	got, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got.ServerURL != "https://example.com" || got.APIKey != "k1" {
		t.Fatalf("config mismatch: %+v", got)
	}

	// Side-effect check: TOML file lives at .satellites/satellites.toml.
	if _, err := os.Stat(filepath.Join(dir, StateDir, ConfigFile)); err != nil {
		t.Fatalf("expected %s on disk: %v", filepath.Join(dir, StateDir, ConfigFile), err)
	}
}

func TestInit_Idempotent(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{ServerURL: "https://example.com", APIKey: "k1"}

	if err := WriteConfig(dir, cfg); err != nil {
		t.Fatalf("first write: %v", err)
	}
	info1, err := os.Stat(filepath.Join(dir, StateDir, ConfigFile))
	if err != nil {
		t.Fatalf("stat 1: %v", err)
	}

	if err := WriteConfig(dir, cfg); err != nil {
		t.Fatalf("second write: %v", err)
	}
	info2, err := os.Stat(filepath.Join(dir, StateDir, ConfigFile))
	if err != nil {
		t.Fatalf("stat 2: %v", err)
	}

	if info1.Size() != info2.Size() {
		t.Fatalf("size diverged across writes: %d vs %d", info1.Size(), info2.Size())
	}

	got, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got != cfg {
		t.Fatalf("config drifted: %+v vs %+v", got, cfg)
	}
}

func TestInit_LoadMissingReturnsDefaults(t *testing.T) {
	dir := t.TempDir() // no satellites.toml inside
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("missing file should be defaults, got err: %v", err)
	}
	if cfg.ServerURL == "" {
		t.Error("defaults: ServerURL should be set")
	}
	if cfg.APIKey != "" {
		t.Errorf("defaults: APIKey should be empty, got %q", cfg.APIKey)
	}
}

func TestInit_SubcommandRegistered(t *testing.T) {
	root := NewRootCmd()
	for _, sub := range root.Commands() {
		if sub.Name() == "init" {
			return
		}
	}
	t.Fatalf("init subcommand not registered on root; commands: %v", root.Commands())
}
