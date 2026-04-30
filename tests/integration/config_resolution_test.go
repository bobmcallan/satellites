package integration

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/moby/moby/api/types/mount"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConfig_ENVOverridesTOML proves the production resolution order
// (env → TOML → defaults) holds for a satellites testcontainer that
// mounts a TOML file AND receives a conflicting env var. The TOML
// declares port=9090; the env declares PORT=8080. The container must
// listen on 8080 (env wins) — not 9090 (TOML), and not 8080-by-default
// (in which case the TOML wasn't read at all).
//
// This is the only integration test of the env-only path going forward.
// Other tests exercise the TOML path via writeTestTOML + startServerWithTOML;
// only this one keeps env in the spotlight, and only to verify the
// resolution order is intact.
func TestConfig_ENVOverridesTOML(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testcontainers test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	tomlPath := writeTestTOML(t, map[string]any{
		// Deliberate mismatch — the env override below must beat this.
		"port":      9090,
		"env":       "dev",
		"log_level": "info",
		"dev_mode":  true,
		"docs_dir":  "/app/docs",
	})

	docsHost := filepath.Join(repoRoot(t), "docs")
	baseURL, logs, stop := startServerWithTOML(t, ctx, startOptions{
		Env: map[string]string{
			"PORT": "8080",
		},
		Mounts: []mount.Mount{{
			Type:     mount.TypeBind,
			Source:   docsHost,
			Target:   "/app/docs",
			ReadOnly: true,
		}},
	}, tomlPath)
	defer stop()

	// Container is reachable via the harness's mapped port, not the
	// listen port itself; the proof that env beat TOML is that the
	// healthz wait succeeded at all (the harness waits on container
	// port 8080, which is what env said). If TOML had won, the
	// container would listen on 9090 and the healthz wait would
	// time out.
	require.NotEmpty(t, baseURL, "container must boot — env must override TOML port")

	bootLogs := logs()
	assert.Contains(t, bootLogs, "config: loaded TOML",
		"boot log must report config: loaded TOML, proving the TOML loader actually ran")
	assert.Contains(t, bootLogs, containerTOMLPath,
		"boot log must include the TOML path the container loaded from")
}
