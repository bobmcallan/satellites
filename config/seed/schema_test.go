package seed

import (
	"strings"
	"testing"
)

func TestParseFrontmatter_EmbeddedInstallArtifact(t *testing.T) {
	s, err := ParseFrontmatter(ClientInstallMarkdown())
	if err != nil {
		t.Fatalf("ParseFrontmatter: %v", err)
	}
	if s.TargetInstallPath == "" {
		t.Error("target_install_path empty")
	}
	if s.TargetConfigPath == "" {
		t.Error("target_config_path empty")
	}
	if s.DefaultConfig.RepoPath == "" {
		t.Error("default_config.repo_path empty")
	}
	if s.AuthBootstrap.Kind == "" {
		t.Error("auth_bootstrap.kind empty")
	}
	if !strings.HasPrefix(s.AuthBootstrap.Command, "satellites") {
		t.Errorf("auth_bootstrap.command = %q, want a 'satellites ...' command", s.AuthBootstrap.Command)
	}
}

func TestParseFrontmatter_MissingOpenDelim(t *testing.T) {
	_, err := ParseFrontmatter([]byte("name: foo\n---\n"))
	if err == nil || !strings.Contains(err.Error(), "opening") {
		t.Fatalf("expected opening-delim error, got %v", err)
	}
}

func TestParseFrontmatter_MissingCloseDelim(t *testing.T) {
	_, err := ParseFrontmatter([]byte("---\nname: foo\n"))
	if err == nil || !strings.Contains(err.Error(), "closing") {
		t.Fatalf("expected closing-delim error, got %v", err)
	}
}

func TestParseFrontmatter_BodyIgnored(t *testing.T) {
	src := []byte(`---
name: x
target_install_path: ./bin/x
target_config_path: ./bin/x.toml
default_config:
  repo_path: .
auth_bootstrap:
  kind: auth_login
  command: x auth login
  env_hint: X_TOKEN
---
# Heading

Body prose that should NOT bleed into the schema.
`)
	s, err := ParseFrontmatter(src)
	if err != nil {
		t.Fatalf("ParseFrontmatter: %v", err)
	}
	if s.Name != "x" {
		t.Errorf("name = %q, want x", s.Name)
	}
	if s.AuthBootstrap.Command != "x auth login" {
		t.Errorf("command = %q, want %q", s.AuthBootstrap.Command, "x auth login")
	}
}
