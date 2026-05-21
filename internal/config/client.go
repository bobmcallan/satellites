package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Client is the runtime configuration for the satellites CLI.
//
// File: .satellites/satellites.toml (colocated under the consumer
// project root). Env overrides applied last.
type Client struct {
	ServerURL string    `toml:"server_url"`
	APIKey    string    `toml:"api_key,omitempty"`
	Log       LogConfig `toml:"log"`
}

// Default file layout under the consumer project's .satellites/ dir.
const (
	ClientStateDir   = ".satellites"
	ClientConfigFile = "satellites.toml"
)

// ClientPath returns the canonical config path for a given project
// root. Pass "." to use the current working directory.
func ClientPath(dir string) string {
	return filepath.Join(dir, ClientStateDir, ClientConfigFile)
}

// ClientDefaults returns in-code defaults.
func ClientDefaults() Client {
	return Client{
		ServerURL: "http://localhost:8080",
		Log:       LogConfig{Level: "info", JSON: false},
	}
}

// LoadClient applies the same layered pattern as LoadServer:
// defaults → optional file → env overrides.
//
// path="" defaults to ClientPath(".") so the typical CLI invocation
// reads .satellites/satellites.toml automatically.
func LoadClient(path string) (Client, error) {
	if path == "" {
		path = ClientPath(".")
	}
	cfg := ClientDefaults()

	if _, err := os.Stat(path); err != nil {
		if !os.IsNotExist(err) {
			return cfg, fmt.Errorf("config: stat %s: %w", path, err)
		}
		// File absent — defaults + env only.
	} else {
		if _, err := toml.DecodeFile(path, &cfg); err != nil {
			return cfg, fmt.Errorf("config: decode %s: %w", path, err)
		}
	}

	cfg.applyClientEnv()
	return cfg, nil
}

func (c *Client) applyClientEnv() {
	if v := os.Getenv("SATELLITES_SERVER_URL"); v != "" {
		c.ServerURL = v
	}
	if v := os.Getenv("SATELLITES_API_KEY"); v != "" {
		c.APIKey = v
	}
	if v := os.Getenv("SATELLITES_LOG_LEVEL"); v != "" {
		c.Log.Level = v
	}
	if v := os.Getenv("SATELLITES_LOG_JSON"); v != "" {
		c.Log.JSON = v == "true" || v == "1"
	}
}

// SaveClient writes the Client config as TOML to <dir>/.satellites/satellites.toml,
// creating the directory if necessary. Idempotent — running twice
// with the same Client produces identical bytes.
func SaveClient(dir string, cfg Client) error {
	stateDir := filepath.Join(dir, ClientStateDir)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("config: mkdir %s: %w", stateDir, err)
	}
	path := filepath.Join(stateDir, ClientConfigFile)
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("config: open %s: %w", tmp, err)
	}
	enc := toml.NewEncoder(f)
	if err := enc.Encode(cfg); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("config: encode %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("config: close %s: %w", tmp, err)
	}
	return os.Rename(tmp, path)
}
