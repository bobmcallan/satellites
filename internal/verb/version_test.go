package verb

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestIsReleaseVersion(t *testing.T) {
	cases := map[string]bool{
		"v0.0.119":            true,
		"0.0.119":             true,
		"dev":                 false,
		"":                    false,
		"0.0.0-dev+ba8c136d":  false,
		"0.0.0-dev+abc-dirty": false,
	}
	for in, want := range cases {
		if got := IsReleaseVersion(in); got != want {
			t.Errorf("IsReleaseVersion(%q) = %v, want %v", in, got, want)
		}
	}
}

// A stamped release build is trusted verbatim — the VCS fallback must
// not rewrite a real tag even when VCS settings are present.
func TestResolveBuildInfoFrom_ReleaseVerbatim(t *testing.T) {
	in := VersionInfo{Version: "v0.0.119", Commit: "abc1234", BuildTime: "2026-06-03"}
	settings := []debug.BuildSetting{
		{Key: "vcs.revision", Value: "deadbeefcafe0000"},
		{Key: "vcs.modified", Value: "true"},
	}
	got := resolveBuildInfoFrom(in, settings)
	if got != in {
		t.Fatalf("release build rewritten: %+v", got)
	}
}

// An unstamped build ("dev") must never report the bare sentinel: the
// VCS fallback turns it into a git-derived 0.0.0-dev+<sha>[-dirty]
// string, backfilling commit + build time.
func TestResolveBuildInfoFrom_DevFallback(t *testing.T) {
	in := VersionInfo{Version: "dev", Commit: "none", BuildTime: "unknown"}
	settings := []debug.BuildSetting{
		{Key: "vcs.revision", Value: "ba8c136da437aabbccdd"},
		{Key: "vcs.modified", Value: "true"},
		{Key: "vcs.time", Value: "2026-06-03T03:53:44Z"},
	}
	got := resolveBuildInfoFrom(in, settings)
	if got.Version != "0.0.0-dev+ba8c136da437-dirty" {
		t.Fatalf("dev fallback version = %q", got.Version)
	}
	if got.Commit != "ba8c136da437" {
		t.Fatalf("commit not backfilled: %q", got.Commit)
	}
	if got.BuildTime != "2026-06-03T03:53:44Z" {
		t.Fatalf("build time not backfilled: %q", got.BuildTime)
	}
}

// A clean (non-dirty) dev build drops the -dirty suffix.
func TestResolveBuildInfoFrom_DevClean(t *testing.T) {
	in := VersionInfo{Version: "dev", Commit: "none", BuildTime: "unknown"}
	settings := []debug.BuildSetting{
		{Key: "vcs.revision", Value: "ba8c136da437"},
		{Key: "vcs.modified", Value: "false"},
	}
	got := resolveBuildInfoFrom(in, settings)
	if strings.HasSuffix(got.Version, "-dirty") {
		t.Fatalf("clean build tagged -dirty: %q", got.Version)
	}
	if got.Version != "0.0.0-dev+ba8c136da437" {
		t.Fatalf("clean dev version = %q", got.Version)
	}
}

// With no embedded VCS info (e.g. a tarball build), there is nothing to
// derive from — fall back to the sentinel rather than inventing a value.
func TestResolveBuildInfoFrom_NoVCS(t *testing.T) {
	in := VersionInfo{Version: "dev", Commit: "none", BuildTime: "unknown"}
	got := resolveBuildInfoFrom(in, nil)
	if got != in {
		t.Fatalf("no-VCS build mutated: %+v", got)
	}
}
