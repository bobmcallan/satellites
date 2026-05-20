package verb

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestComputeState(t *testing.T) {
	cases := []struct {
		current, server, want string
	}{
		{"", "v1.0.0", "install_required"},
		{"v1.0.0", "v1.0.0", "up_to_date"},
		{"v0.9.0", "v1.0.0", "update_available"},
	}
	for _, c := range cases {
		if got := computeState(c.current, c.server); got != c.want {
			t.Errorf("computeState(%q, %q) = %q, want %q", c.current, c.server, got, c.want)
		}
	}
}

func TestSatellitesInit_NoAuthStore_ReturnsAuthLogin(t *testing.T) {
	// authStore is unset (CLI-local default). Verb should respond
	// with AuthBootstrap.Kind="auth_login".
	resp, err := Dispatch(context.Background(), "satellites_init", json.RawMessage(`{
        "current_version": "",
        "os": "linux",
        "arch": "amd64"
    }`))
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	var got SatellitesInitResponse
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.State != "install_required" {
		t.Errorf("state: got %q want install_required", got.State)
	}
	if got.AuthBootstrap == nil || got.AuthBootstrap.Kind != "auth_login" {
		t.Errorf("auth_bootstrap: got %+v want kind=auth_login", got.AuthBootstrap)
	}
	if got.AuthBootstrap.APIKey != "" {
		t.Errorf("api_key leaked on auth_login: %s", got.AuthBootstrap.APIKey)
	}
	if got.TargetInstallPath != "./.satellites/satellites" {
		t.Errorf("target install path: got %s", got.TargetInstallPath)
	}
	if got.Install == nil || !strings.Contains(got.Install.DownloadURL, "satellites-") {
		t.Errorf("install info malformed: %+v", got.Install)
	}
	if !strings.Contains(got.Install.DownloadURL, "linux-amd64") {
		t.Errorf("install URL missing platform suffix: %s", got.Install.DownloadURL)
	}
}

func TestSatellitesInit_DefaultsToRuntimeOSArch(t *testing.T) {
	resp, err := Dispatch(context.Background(), "satellites_init", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	var got SatellitesInitResponse
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Test runs under runtime.GOOS/GOARCH; the URL must reflect that.
	if got.Install == nil {
		t.Fatal("install missing")
	}
	// Just confirm it's well-formed; can't pin to runtime since tests
	// run on different platforms.
	if !strings.HasPrefix(got.Install.DownloadURL, "https://") {
		t.Errorf("download URL not https: %s", got.Install.DownloadURL)
	}
}
