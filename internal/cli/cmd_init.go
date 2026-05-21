package cli

import (
	"fmt"

	"github.com/bobmcallan/satellites/internal/config"
	"github.com/spf13/cobra"
)

// Re-export for back-compat with sty_60c48d81 (satellites_init MCP verb
// references the colocated state directory). These now live in
// internal/config; the alias here means external callers don't break.
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
		dir       string
		useOAuth  bool
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Bootstrap a project's .satellites/ state directory",
		Long: `init writes .satellites/satellites.toml with the server URL and
(optionally) an API key. Idempotent: running twice with the same
args writes identical bytes.

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
			cfg := config.ClientDefaults()
			cfg.ServerURL = serverURL
			cfg.APIKey = apiKey
			return config.SaveClient(dir, cfg)
		},
	}
	cmd.Flags().StringVar(&apiKey, "api-key", "", "API key for authenticating against satellites-server")
	cmd.Flags().StringVar(&serverURL, "server-url", "http://localhost:8080", "satellites-server URL")
	cmd.Flags().StringVar(&dir, "dir", ".", "Project root (state dir created as <dir>/.satellites/)")
	cmd.Flags().BoolVar(&useOAuth, "oauth", false, "Use interactive OAuth flow (deferred — currently errors with a pointer to --api-key)")

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
