package cliconfig

import (
	"path/filepath"
	"testing"
)

func TestResolveLogDir(t *testing.T) {
	cases := []struct {
		name, logPath, repoRoot, want string
	}{
		{"default anchors to repo root", "", "/repo", filepath.Join("/repo", DefaultLogDir)},
		{"relative resolves against repo root", "logs/sessions", "/repo", filepath.Join("/repo", "logs/sessions")},
		{"absolute used verbatim", "/var/log/sat", "/repo", "/var/log/sat"},
		{"empty repo root falls back to CWD", "", "", filepath.Join(".", DefaultLogDir)},
	}
	for _, c := range cases {
		got := Config{LogPath: c.logPath}.ResolveLogDir(c.repoRoot)
		if got != c.want {
			t.Errorf("%s: ResolveLogDir(%q) with log_path=%q = %q, want %q", c.name, c.repoRoot, c.logPath, got, c.want)
		}
	}
}

func TestRepoRootFromConfigPath(t *testing.T) {
	if got := RepoRootFromConfigPath("/repo/.satellites/satellites.toml"); got != "/repo" {
		t.Errorf("RepoRootFromConfigPath = %q, want /repo", got)
	}
	if got := RepoRootFromConfigPath(""); got != "." {
		t.Errorf("empty path: RepoRootFromConfigPath = %q, want .", got)
	}
}
