package verb

import (
	"context"
	"encoding/json"
	"runtime/debug"
	"strings"
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

// IsReleaseVersion reports whether v is a stamped release version — a
// real tag, not the unstamped "dev" sentinel or empty. Update uses this
// to tell "on a dev/untagged build" apart from "behind the latest
// release" (sty_d6c95262).
func IsReleaseVersion(v string) bool {
	return v != "" && v != "dev" && !strings.HasPrefix(v, "0.0.0-dev")
}

// resolveBuildInfo returns the effective (version, commit, build time),
// reading the VCS stamp Go embeds in every build via debug.ReadBuildInfo.
func resolveBuildInfo() VersionInfo {
	var settings []debug.BuildSetting
	if bi, ok := debug.ReadBuildInfo(); ok {
		settings = bi.Settings
	}
	return resolveBuildInfoFrom(VersionInfo{Version: Version, Commit: Commit, BuildTime: BuildTime}, settings)
}

// resolveBuildInfoFrom is the testable core. A release binary is
// ldflag-stamped, so its values are returned verbatim. A binary built
// any other way — a bare `go build`, an IDE run, a hand-built dev
// binary — leaves Version=="dev"; for those we fall back to the embedded
// VCS settings so `version` reports a real, git-derived string instead
// of the bare "dev" (sty_d6c95262).
func resolveBuildInfoFrom(info VersionInfo, settings []debug.BuildSetting) VersionInfo {
	if IsReleaseVersion(info.Version) {
		return info // ldflag-stamped release build — trust it verbatim.
	}
	var rev, dirty, vcsTime string
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value
		case "vcs.time":
			vcsTime = s.Value
		}
	}
	if rev == "" {
		return info // no embedded VCS info (e.g. built from a tarball).
	}
	short := rev
	if len(short) > 12 {
		short = short[:12]
	}
	v := "0.0.0-dev+" + short
	if dirty == "true" {
		v += "-dirty"
	}
	info.Version = v
	if info.Commit == "" || info.Commit == "none" {
		info.Commit = short
	}
	if (info.BuildTime == "" || info.BuildTime == "unknown") && vcsTime != "" {
		info.BuildTime = vcsTime
	}
	return info
}

func init() {
	Register(&Verb{
		Name:        "version",
		Description: "Return build info (version, commit, build time)",
		Invoke: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
			return json.Marshal(resolveBuildInfo())
		},
	})
}
