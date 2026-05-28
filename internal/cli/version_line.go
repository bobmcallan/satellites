package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bobmcallan/satellites/internal/verb"
)

// versionLine is the single source for the build-info string printed by
// both the `version` subcommand and the root `--version` / `-v` flag.
// Sourced through verb.Dispatch so the two entry points cannot drift.
func versionLine() string {
	resp, err := verb.Dispatch(context.Background(), "version", nil)
	if err != nil {
		return fmt.Sprintf("satellites (version verb error: %v)", err)
	}
	var info verb.VersionInfo
	if err := json.Unmarshal(resp, &info); err != nil {
		return fmt.Sprintf("satellites (version decode error: %v)", err)
	}
	return fmt.Sprintf("satellites %s (commit %s, built %s)", info.Version, info.Commit, info.BuildTime)
}
