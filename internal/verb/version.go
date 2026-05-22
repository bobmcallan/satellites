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
//
// CLIVersion is the operator-facing CLI release version, ldflag-stamped
// on the satellites-server binary so the install schema can advertise
// "fetch satellites-vX.Y.Z" independent of the server's own build
// version. The CLI binary leaves CLIVersion empty; CLIVersionEffective
// falls back to Version, which on the CLI is the CLI build itself.
var (
	Version    = "dev"
	CLIVersion = ""
	Commit     = "none"
	BuildTime  = "unknown"
)

// CLIVersionEffective returns CLIVersion when ldflag-stamped, otherwise
// Version. Keeps templated install schemas working on pre-S5 builds
// where only Version was stamped.
func CLIVersionEffective() string {
	if CLIVersion != "" {
		return CLIVersion
	}
	return Version
}

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
