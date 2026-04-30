package integration

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// containerTOMLPath is where the TOML helper bind-mounts the per-test
// satellites.toml inside the container. Production binaries read TOML
// from `./satellites.toml` or the path named by SATELLITES_CONFIG; the
// helper sets SATELLITES_CONFIG to this fixed target so the binary
// resolves it explicitly.
const containerTOMLPath = "/app/satellites.toml"

// writeTestTOML serialises cfg as TOML and writes it to a per-test
// temporary file, returning the absolute host path.
//
// cfg is a free-shape map so tests can set any production TOML key
// without depending on the internal/config Config struct (internal/ is
// not importable from tests/integration). Keys must match the
// `toml:"…"` tags on internal/config.Config.
func writeTestTOML(t *testing.T, cfg map[string]any) string {
	t.Helper()
	body, err := toml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal test toml: %v", err)
	}
	path := filepath.Join(t.TempDir(), "satellites.toml")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write test toml %s: %v", path, err)
	}
	return path
}

// startServerWithTOML boots the satellites testcontainer with a
// bind-mounted TOML config + SATELLITES_CONFIG=/app/satellites.toml.
// Returns (baseURL, logs, stop):
//   - baseURL — http://<host>:<mapped> for hitting the container.
//   - logs — closure that drains the container stdout+stderr into a
//     string. Used to assert "config: loaded TOML" appears at boot.
//   - stop — terminate closure the caller defers.
//
// opts.Env entries take precedence — the right place for secrets
// (GEMINI_API_KEY, SATELLITES_API_KEYS) and per-test overrides
// (DB_DSN, EMBEDDINGS_*) that aren't carried by the Config struct or
// shouldn't be checked into a TOML file.
//
// The mount is ReadOnly so the in-container binary cannot mutate the
// host fixture. Caller-supplied mounts in opts.Mounts are preserved.
func startServerWithTOML(
	t *testing.T,
	ctx context.Context,
	opts startOptions,
	tomlHostPath string,
) (string, func() string, func()) {
	t.Helper()
	root := repoRoot(t)

	env := map[string]string{
		"PORT":              "8080",
		"ENV":               "dev",
		"LOG_LEVEL":         "info",
		"DEV_MODE":          "true",
		"SATELLITES_CONFIG": containerTOMLPath,
	}
	for k, v := range opts.Env {
		env[k] = v
	}

	mounts := append([]mount.Mount{}, opts.Mounts...)
	mounts = append(mounts, mount.Mount{
		Type:     mount.TypeBind,
		Source:   tomlHostPath,
		Target:   containerTOMLPath,
		ReadOnly: true,
	})

	req := testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    root,
			Dockerfile: "docker/Dockerfile",
			KeepImage:  true,
		},
		ExposedPorts: []string{"8080/tcp"},
		Env:          env,
		WaitingFor: wait.ForHTTP("/healthz").
			WithPort("8080/tcp").
			WithStartupTimeout(120 * time.Second),
	}
	if opts.Network != "" {
		req.Networks = []string{opts.Network}
	}
	mountsCopy := mounts
	req.HostConfigModifier = func(hc *container.HostConfig) {
		hc.Mounts = append(hc.Mounts, mountsCopy...)
	}

	cont, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start container with toml: %v", err)
	}
	host, err := cont.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	mapped, err := cont.MappedPort(ctx, "8080/tcp")
	if err != nil {
		t.Fatalf("container port: %v", err)
	}
	baseURL := fmt.Sprintf("http://%s:%s", host, mapped.Port())

	logs := func() string {
		logCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		r, err := cont.Logs(logCtx)
		if err != nil {
			t.Logf("container.Logs error: %v", err)
			return ""
		}
		defer r.Close()
		body, err := io.ReadAll(r)
		if err != nil {
			t.Logf("container.Logs read error: %v", err)
			return ""
		}
		return string(body)
	}
	stop := func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = cont.Terminate(stopCtx)
	}
	return baseURL, logs, stop
}
