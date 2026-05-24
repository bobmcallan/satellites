package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestServerDefaults(t *testing.T) {
	cfg := ServerDefaults()
	if cfg.Addr != ":8080" {
		t.Errorf("addr default: got %q", cfg.Addr)
	}
	if cfg.Dev {
		t.Error("dev default must be false")
	}
	if cfg.Log.Level != "info" {
		t.Errorf("log level default: got %q", cfg.Log.Level)
	}
}

func TestLoadServer_NoFileDefaultsOnly(t *testing.T) {
	t.Setenv("SATELLITES_LISTEN_ADDR", "")
	cfg, err := LoadServer("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":8080" {
		t.Errorf("addr: got %q", cfg.Addr)
	}
}

func TestLoadServer_EnvOverridesDefault(t *testing.T) {
	t.Setenv("SATELLITES_LISTEN_ADDR", ":9090")
	t.Setenv("SATELLITES_DEV", "true")
	cfg, err := LoadServer("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":9090" {
		t.Errorf("addr: got %q", cfg.Addr)
	}
	if !cfg.Dev {
		t.Error("dev: env should have flipped to true")
	}
}

func TestLoadServer_FileThenEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.toml")
	if err := os.WriteFile(path, []byte(`
addr = ":7070"
dsn  = "postgres://from-file/db"
dev  = true

[log]
level = "debug"
json  = true
`), 0o600); err != nil {
		t.Fatal(err)
	}

	// File sets addr=:7070; env overrides to :8888.
	t.Setenv("SATELLITES_LISTEN_ADDR", ":8888")
	cfg, err := LoadServer(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":8888" {
		t.Errorf("addr (env over file): got %q", cfg.Addr)
	}
	if cfg.DSN != "postgres://from-file/db" {
		t.Errorf("dsn (from file): got %q", cfg.DSN)
	}
	if !cfg.Dev {
		t.Error("dev: file should have set it")
	}
	if cfg.Log.Level != "debug" || !cfg.Log.JSON {
		t.Errorf("log from file: %+v", cfg.Log)
	}
}
