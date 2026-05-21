package verb

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestSatellitesInit_ReadsEmbeddedSchema confirms the verb wires the
// embedded install-schema markdown's frontmatter into the response
// (rather than the old Go-constant defaults).
func TestSatellitesInit_ReadsEmbeddedSchema(t *testing.T) {
	out, err := Dispatch(context.Background(), "satellites_init",
		json.RawMessage(`{"os":"linux","arch":"amd64"}`))
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	var resp struct {
		TargetInstallPath string         `json:"target_install_path"`
		TargetConfigPath  string         `json:"target_config_path"`
		DefaultConfig     map[string]any `json:"default_config"`
		AuthBootstrap     map[string]any `json:"auth_bootstrap"`
		Install           map[string]any `json:"install"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.HasPrefix(resp.TargetInstallPath, "./.satellites/") {
		t.Errorf("target_install_path = %q, want ./.satellites/ prefix from embedded schema",
			resp.TargetInstallPath)
	}
	if _, ok := resp.DefaultConfig["repo_path"]; !ok {
		t.Errorf("default_config missing repo_path: %v", resp.DefaultConfig)
	}
	if _, ok := resp.DefaultConfig["worktree_root"]; !ok {
		t.Errorf("default_config missing worktree_root (not in old hard-coded constants)")
	}
	if kind, _ := resp.AuthBootstrap["kind"].(string); kind == "" {
		t.Errorf("auth_bootstrap.kind empty: %v", resp.AuthBootstrap)
	}
	if cmd, _ := resp.AuthBootstrap["command"].(string); !strings.HasPrefix(cmd, "satellites") {
		t.Errorf("auth_bootstrap.command = %q, want 'satellites ...' from embedded schema", cmd)
	}
	if url, _ := resp.Install["download_url"].(string); !strings.HasSuffix(url, "-linux-amd64") {
		t.Errorf("install.download_url = %q, want -linux-amd64 suffix", url)
	}
}
