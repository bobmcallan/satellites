package verb

import (
	"context"
	"encoding/json"
)

// VersionInfo is the response shape for the version verb.
type VersionInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
}

// Build info — overridden via -ldflags at release time
// (sty_d3270775). These are the single source of truth across both
// satellites and satellites-server binaries.
var (
	Version   = "dev"
	Commit    = "none"
	BuildTime = "unknown"
)

func init() {
	Register(&Verb{
		Name:        "version",
		Description: "Return build info (version, commit, build time)",
		Invoke: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
			return json.Marshal(VersionInfo{
				Version:   Version,
				Commit:    Commit,
				BuildTime: BuildTime,
			})
		},
	})
}
