//go:build integration

package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/config/seed"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestClientInstallSchema_DocumentBacked verifies that, after wiring
// the document-store SchemaSource, seed.ClientInstallSchema() returns
// the same operator-relevant fields (paths, defaults, auth bootstrap)
// as the embedded artifact AND additionally populates the install
// block with rendered URLs from the system-variables resolver.
//
// This is the S6 dogfood gate translated into a test: satellites_init
// still works (because the schema's existing fields are unchanged) and
// the new install.* template fields render correctly when the document
// store backs the schema.
func TestClientInstallSchema_DocumentBacked(t *testing.T) {
	env := testbootstrap.SetUp(t)
	docStore := document.New(env.DB)
	verb.SetDocumentStore(docStore)
	t.Cleanup(func() {
		verb.SetDocumentStore(nil)
		verb.SetSystemVariableResolver(nil, nil)
		seed.SetClientInstallSchemaSource(nil)
	})

	// Seed the embedded artifact as a scope=system document.
	body := string(seed.ClientInstallMarkdown())
	if err := document.SeedSystem(context.Background(), docStore, "satellites_client_install", body, "system:seed", time.Now()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Wire a minimal system-variables resolver.
	resolved := map[string]string{
		"version":     verb.Version,
		"cli_version": verb.CLIVersionEffective(),
		"os":          "linux",
		"arch":        "amd64",
		"server_url":  "https://test.example",
	}
	verb.SetSystemVariableResolver(
		func(_ context.Context, name string) (string, bool) {
			v, ok := resolved[name]
			return v, ok
		},
		func(context.Context) []string {
			return []string{"version", "cli_version", "os", "arch", "server_url"}
		},
	)

	// Install the document-backed schema source mirroring what
	// satellites-server boot wires.
	seed.SetClientInstallSchemaSource(func(ctx context.Context) ([]byte, error) {
		res, err := docStore.Get(ctx, document.Key{Scope: document.ScopeSystem, Name: "satellites_client_install"}, document.GetOptions{})
		if err != nil {
			return nil, err
		}
		v := res.Versions[0]
		parsed := document.Parse(v.Body)
		rendered, _ := parsed.Render(document.ResolverFunc(func(name string) (string, bool) {
			val, ok := resolved[name]
			return val, ok
		}))
		return []byte(rendered), nil
	})

	t.Run("existing fields unchanged", func(t *testing.T) {
		s, err := seed.ClientInstallSchema()
		if err != nil {
			t.Fatalf("schema: %v", err)
		}
		if s.TargetInstallPath != "./.satellites/satellites" {
			t.Errorf("target_install_path: got %s", s.TargetInstallPath)
		}
		if s.TargetConfigPath != "./.satellites/satellites.toml" {
			t.Errorf("target_config_path: got %s", s.TargetConfigPath)
		}
		if s.AuthBootstrap.Kind != "auth_login" {
			t.Errorf("auth_bootstrap.kind: got %s", s.AuthBootstrap.Kind)
		}
		if s.AuthBootstrap.Command != "satellites auth login" {
			t.Errorf("auth_bootstrap.command: got %s", s.AuthBootstrap.Command)
		}
	})

	t.Run("install.download_url renders against system vars", func(t *testing.T) {
		s, err := seed.ClientInstallSchema()
		if err != nil {
			t.Fatalf("schema: %v", err)
		}
		// Resolved against the stub above: cli_version=verb.CLIVersionEffective(), os=linux, arch=amd64.
		want := "https://github.com/bobmcallan/satellites/releases/latest/download/satellites-" + verb.CLIVersionEffective() + "-linux-amd64"
		if s.Install.DownloadURL != want {
			t.Errorf("install.download_url: got %s want %s", s.Install.DownloadURL, want)
		}
		if !strings.HasSuffix(s.Install.SHA256URL, ".sha256") {
			t.Errorf("install.sha256_url shape: got %s", s.Install.SHA256URL)
		}
	})
}
