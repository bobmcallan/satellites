package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// StateDir is the colocated state directory (V5 .satellites/ pattern).
const (
	StateDir   = ".satellites"
	ConfigFile = "config.json"
)

// Config is the on-disk shape of .satellites/config.json. OAuth-driven
// fields land in sty_9b3e355c.
type Config struct {
	ServerURL string `json:"server_url"`
	APIKey    string `json:"api_key,omitempty"`
}

func init() {
	var (
		apiKey    string
		serverURL string
		dir       string
		useOAuth  bool
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Bootstrap a project's .satellites/ state directory",
		Long: `init writes .satellites/config.json with the server URL and
(optionally) an API key. Idempotent: running twice with the same
args overwrites with the same content.

Auth modes:
  --api-key <key>   non-interactive; agents + scripts use this
  --oauth           interactive browser flow (deferred — see below)

OAuth client-side flow is a partial-delivery follow-up of sty_9b3e355c:
the server-side scaffold exists, but launching the browser + running a
local callback listener lands in a separate story. For now, use
--api-key (in dev mode, sk_dev_admin or sk_dev_user).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if useOAuth {
				return fmt.Errorf("--oauth client-side flow deferred; use --api-key for now (sty_9b3e355c follow-up)")
			}
			return WriteConfig(dir, Config{ServerURL: serverURL, APIKey: apiKey})
		},
	}
	cmd.Flags().StringVar(&apiKey, "api-key", "", "API key for authenticating against satellites-server")
	cmd.Flags().StringVar(&serverURL, "server-url", "http://localhost:8080", "satellites-server URL")
	cmd.Flags().StringVar(&dir, "dir", ".", "Project root (state dir created as <dir>/.satellites/)")
	cmd.Flags().BoolVar(&useOAuth, "oauth", false, "Use interactive OAuth flow (deferred — currently errors with a pointer to --api-key)")

	register(cmd)
}

// WriteConfig writes config.json to <dir>/.satellites/ idempotently.
// Exported for tests and for sty_60c48d81 (satellites_init MCP verb).
func WriteConfig(dir string, cfg Config) error {
	stateDir := filepath.Join(dir, StateDir)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", stateDir, err)
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	b = append(b, '\n')
	cfgPath := filepath.Join(stateDir, ConfigFile)
	if err := os.WriteFile(cfgPath, b, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", cfgPath, err)
	}
	return nil
}

// LoadConfig reads <dir>/.satellites/config.json. Exported for substrate
// verb dispatch (needs the API key + server URL to talk to the server).
func LoadConfig(dir string) (Config, error) {
	var cfg Config
	cfgPath := filepath.Join(dir, StateDir, ConfigFile)
	b, err := os.ReadFile(cfgPath)
	if err != nil {
		return cfg, fmt.Errorf("read %s: %w", cfgPath, err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", cfgPath, err)
	}
	return cfg, nil
}
