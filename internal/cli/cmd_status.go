// `satellites status` — a typed wrapper over the system_status MCP verb.
// It reports who the server is (version/commit/build), whether the
// substrate DB is reachable, and how long the instance has been up, plus
// the local CLI build for a quick skew check. The CLI's own version is
// sent as client_version (echo-only); --json prints the raw response.
//
// No new MCP verb is introduced here — system_status is the sanctioned
// introspection verb (see the no-new-mcp-verbs principle); this command is
// only the client-side ergonomic wrapper.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/spf13/cobra"
)

// statusView mirrors verb.SystemStatusResponse for rendering. Kept local so
// the CLI decodes only the fields it prints (the response is verb-owned).
type statusView struct {
	Server struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
		BuiltAt string `json:"built_at"`
	} `json:"server"`
	Client *struct {
		Version string `json:"version"`
	} `json:"client"`
	DB struct {
		OK        bool   `json:"ok"`
		LatencyMS int64  `json:"latency_ms"`
		Error     string `json:"error"`
	} `json:"db"`
	UptimeSeconds int64  `json:"uptime_seconds"`
	StartedAt     string `json:"started_at"`
}

func init() {
	var (
		configArg string
		userArg   string
		asJSON    bool
	)
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Server + client version, substrate DB health, and uptime (system_status)",
		Long:  "Typed wrapper over the system_status MCP verb. Reports server version/commit/build, substrate DB reachability, process uptime, and the local CLI build.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			// Send the CLI's own build as client_version (echo-only) so the
			// operator can eyeball server↔client skew in one call.
			req, err := json.Marshal(verb.SystemStatusRequest{ClientVersion: verb.CLIVersionEffective()})
			if err != nil {
				return err
			}
			resp, err := dispatchVerb(ctx, "system_status", req, configArg, userArg)
			if err != nil {
				return err
			}
			if asJSON {
				fmt.Fprintln(cmd.OutOrStdout(), string(resp))
				return nil
			}
			var s statusView
			if err := json.Unmarshal(resp, &s); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
			renderStatus(cmd.OutOrStdout(), s)
			return nil
		},
	}
	cmd.Flags().StringVar(&configArg, "config", "", "Path to satellites.toml (overrides $SATELLITES_CONFIG / .satellites/satellites.toml walk-up).")
	cmd.Flags().StringVar(&userArg, "user", "", "Caller user id (overrides $SATELLITES_USER_ID). Stamped onto verbs when dispatching in-process.")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the raw system_status JSON response")
	register(cmd)
}

// renderStatus pretty-prints the status view as an aligned key/value block.
func renderStatus(out io.Writer, s statusView) {
	fmt.Fprintf(out, "server   %s (%s, built %s)\n", s.Server.Version, s.Server.Commit, s.Server.BuiltAt)
	if s.Client != nil {
		fmt.Fprintf(out, "client   %s\n", s.Client.Version)
	}
	db := "ok"
	if !s.DB.OK {
		db = "DOWN"
		if s.DB.Error != "" {
			db += ": " + s.DB.Error
		}
	}
	fmt.Fprintf(out, "db       %s (%dms)\n", db, s.DB.LatencyMS)
	fmt.Fprintf(out, "uptime   %ds (since %s)\n", s.UptimeSeconds, s.StartedAt)
}
