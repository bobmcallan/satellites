package cli

import (
	"fmt"

	"github.com/bobmcallan/satellites/internal/config"
	"github.com/spf13/cobra"
)

// Re-export so external callers can address the canonical state-dir
// path without importing internal/config. The values live in
// internal/config (single source of truth).
const (
	StateDir   = config.ClientStateDir
	ConfigFile = config.ClientConfigFile
)

// Config is the on-disk shape, now stored as TOML via internal/config.
// Kept as a thin alias to keep existing external callers compiling.
type Config = config.Client

func init() {
	var (
		apiKey    string
		serverURL string
		projectID string
		dir       string
		useOAuth  bool
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Bootstrap a project's .satellites/ state directory",
		Long: `init writes .satellites/satellites.toml with the server URL,
(optionally) an API key, and (optionally) a project_id. Idempotent:
running twice with the same args writes identical bytes.

Auth modes:
  --api-key <key>     non-interactive; agents + scripts use this
  --oauth             interactive browser flow (deferred)

Operational verbs (story, document, …) require project_id either in
the TOML or via --project-id at the call site. Resolve a project
from a git remote with: satellites project match --remote <url>.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if useOAuth {
				return fmt.Errorf("--oauth client-side flow deferred; use --api-key for now")
			}
			cfg := config.ClientDefaults()
			cfg.ServerURL = serverURL
			cfg.APIKey = apiKey
			cfg.ProjectID = projectID
			return config.SaveClient(dir, cfg)
		},
	}
	cmd.Flags().StringVar(&apiKey, "api-key", "", "API key for authenticating against satellites-server")
	cmd.Flags().StringVar(&serverURL, "server-url", "http://localhost:8080", "satellites-server URL")
	cmd.Flags().StringVar(&projectID, "project-id", "", "Default project_id for operational verbs (empty if unknown at init time)")
	cmd.Flags().StringVar(&dir, "dir", ".", "Project root (state dir created as <dir>/.satellites/)")
	cmd.Flags().BoolVar(&useOAuth, "oauth", false, "Use interactive OAuth flow (deferred)")

	register(cmd)
}

// WriteConfig is preserved for callers (tests) that historically used
// it. Delegates to config.SaveClient so .satellites/satellites.toml
// is always the result on disk.
func WriteConfig(dir string, cfg Config) error {
	return config.SaveClient(dir, cfg)
}

// LoadConfig is preserved for the same back-compat reason.
func LoadConfig(dir string) (Config, error) {
	return config.LoadClient(config.ClientPath(dir))
}
