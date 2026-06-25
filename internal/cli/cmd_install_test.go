package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// newInstallAsset serves a binary payload at /bin and its sha256 at /sha
// (zeroed when corrupt), returning the two URLs.
func newInstallAsset(t *testing.T, payload []byte, corrupt bool) (binURL, shaURL string, stop func()) {
	t.Helper()
	sum := sha256.Sum256(payload)
	hexSum := hex.EncodeToString(sum[:])
	if corrupt {
		hexSum = hex.EncodeToString(make([]byte, 32))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/bin", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) })
	mux.HandleFunc("/sha", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintf(w, "%s  satellites\n", hexSum) })
	srv := httptest.NewServer(mux)
	return srv.URL + "/bin", srv.URL + "/sha", srv.Close
}

func TestAssetName_OsArch(t *testing.T) {
	got := assetName("v0.0.121")
	want := fmt.Sprintf("satellites-v0.0.121-%s-%s", runtime.GOOS, runtime.GOARCH)
	if got != want {
		t.Errorf("assetName = %q, want %q", got, want)
	}
}

func TestReleaseAssetURLs(t *testing.T) {
	bin, sha := releaseAssetURLs("v0.0.121", "satellites-v0.0.121-linux-amd64")
	wantBin := "https://github.com/" + releaseRepo + "/releases/download/v0.0.121/satellites-v0.0.121-linux-amd64"
	if bin != wantBin {
		t.Errorf("binURL = %q, want %q", bin, wantBin)
	}
	if sha != wantBin+".sha256" {
		t.Errorf("shaURL = %q, want %q.sha256", sha, wantBin)
	}
}

func TestInstallTarget_Global(t *testing.T) {
	home := t.TempDir()
	got, err := installTarget(false, "", home)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".local", "bin", "satellites")
	if got != want {
		t.Errorf("global target = %q, want %q", got, want)
	}
}

// AC4: --local resolves to the repo's .satellites/satellites via the config
// path (config walk-up is cliconfig's job; here we pin the explicit-path case).
func TestInstallTarget_LocalFromConfig(t *testing.T) {
	repo := t.TempDir()
	satDir := filepath.Join(repo, ".satellites")
	if err := os.MkdirAll(satDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(satDir, "satellites.toml")
	if err := os.WriteFile(cfgPath, []byte("server_url = \"https://example\"\nproject_id = \"proj_x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := installTarget(true, cfgPath, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(satDir, "satellites")
	if got != want {
		t.Errorf("local target = %q, want %q", got, want)
	}
}

// AC4/AC7: a global binary run inside a repo resolves that repo's
// .satellites/satellites.toml by walking up from CWD — so --local with no
// explicit --config finds the repo from a nested working directory.
func TestInstallTarget_LocalWalkUpFromCWD(t *testing.T) {
	t.Setenv("SATELLITES_CONFIG", "") // don't let an ambient config win the walk-up
	repo := t.TempDir()
	satDir := filepath.Join(repo, ".satellites")
	if err := os.MkdirAll(satDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(satDir, "satellites.toml"), []byte("server_url = \"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)

	got, err := installTarget(true, "", t.TempDir())
	if err != nil {
		t.Fatalf("installTarget: %v", err)
	}
	if !strings.HasSuffix(filepath.ToSlash(got), ".satellites/satellites") {
		t.Errorf("local target via walk-up = %q, want a .satellites/satellites path", got)
	}
}

func TestRunInstall_PlacesWhenAbsent(t *testing.T) {
	payload := []byte("NEW BINARY")
	bin, sha, stop := newInstallAsset(t, payload, false)
	defer stop()
	target := filepath.Join(t.TempDir(), "sub", "satellites") // dir created by install

	err := runInstall(context.Background(), io.Discard, installPlan{
		TargetPath: target, Tag: "v0.0.9", AssetURL: bin, ShaURL: sha,
	})
	if err != nil {
		t.Fatalf("runInstall: %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != string(payload) {
		t.Errorf("binary not placed: %q", got)
	}
	if fi, _ := os.Stat(target); fi.Mode().Perm()&0o100 == 0 {
		t.Error("placed binary is not executable")
	}
}

func TestRunInstall_NoopSameVersion(t *testing.T) {
	target := filepath.Join(t.TempDir(), "satellites")
	_ = os.WriteFile(target, []byte("ORIG"), 0o755)
	out := &strings.Builder{}
	// No asset server needed — same version must short-circuit before download.
	err := runInstall(context.Background(), out, installPlan{
		TargetPath: target, Tag: "v0.0.9", ExistingVersion: "v0.0.9",
	})
	if err != nil {
		t.Fatalf("runInstall: %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "ORIG" {
		t.Errorf("no-op must not rewrite, got %q", got)
	}
	if !strings.Contains(out.String(), "already installed") {
		t.Errorf("expected already-installed message, got %q", out.String())
	}
}

func TestRunInstall_RefuseClobberWithoutForce(t *testing.T) {
	target := filepath.Join(t.TempDir(), "satellites")
	_ = os.WriteFile(target, []byte("ORIG"), 0o755)
	err := runInstall(context.Background(), io.Discard, installPlan{
		TargetPath: target, Tag: "v0.0.9", ExistingVersion: "v0.0.4", Force: false,
	})
	if err == nil {
		t.Fatal("expected refuse-clobber error")
	}
	if got, _ := os.ReadFile(target); string(got) != "ORIG" {
		t.Errorf("refused install must leave binary intact, got %q", got)
	}
}

func TestRunInstall_ForceOverwrites(t *testing.T) {
	payload := []byte("v9 BINARY")
	bin, sha, stop := newInstallAsset(t, payload, false)
	defer stop()
	target := filepath.Join(t.TempDir(), "satellites")
	_ = os.WriteFile(target, []byte("ORIG"), 0o755)

	err := runInstall(context.Background(), io.Discard, installPlan{
		TargetPath: target, Tag: "v0.0.9", AssetURL: bin, ShaURL: sha,
		ExistingVersion: "v0.0.4", Force: true,
	})
	if err != nil {
		t.Fatalf("runInstall --force: %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != string(payload) {
		t.Errorf("force install did not overwrite, got %q", got)
	}
}

func TestRunInstall_ShaMismatchLeavesIntact(t *testing.T) {
	bin, sha, stop := newInstallAsset(t, []byte("tampered"), true)
	defer stop()
	dir := t.TempDir()
	target := filepath.Join(dir, "satellites")
	_ = os.WriteFile(target, []byte("ORIG"), 0o755)

	err := runInstall(context.Background(), io.Discard, installPlan{
		TargetPath: target, Tag: "v0.0.9", AssetURL: bin, ShaURL: sha,
		ExistingVersion: "v0.0.4", Force: true,
	})
	if err == nil {
		t.Fatal("expected checksum-mismatch error")
	}
	if got, _ := os.ReadFile(target); string(got) != "ORIG" {
		t.Errorf("binary must be intact on checksum failure, got %q", got)
	}
	// No leftover temp files.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("expected only the original binary, found %d entries", len(entries))
	}
}

func TestGlobalBinaryPath(t *testing.T) {
	if got := globalBinaryPath("/home/u"); got != "/home/u/.local/bin/satellites" {
		t.Errorf("globalBinaryPath = %q", got)
	}
}

// TestEnsureScaffoldOnLocalInstall pins sty_9755c533: an uninitialized repo gets
// the init scaffold (so .mcp.json appears); an already-initialized repo is left
// untouched when the operator declines (confirm=false).
func TestEnsureScaffoldOnLocalInstall(t *testing.T) {
	t.Run("uninitialized → scaffolds and writes .mcp.json", func(t *testing.T) {
		repo := t.TempDir()
		var out bytes.Buffer
		if err := ensureScaffoldOnLocalInstall(&out, repo, func(string) bool { return false }); err != nil {
			t.Fatalf("ensureScaffold: %v", err)
		}
		if _, err := os.Stat(filepath.Join(repo, ".mcp.json")); err != nil {
			t.Errorf(".mcp.json not written by scaffold: %v\n%s", err, out.String())
		}
		if _, err := os.Stat(filepath.Join(repo, ".satellites", "satellites.toml")); err != nil {
			t.Errorf(".satellites/satellites.toml not written by scaffold: %v", err)
		}
	})

	t.Run("already initialized + declined → no clobber, steers", func(t *testing.T) {
		repo := t.TempDir()
		if err := os.MkdirAll(filepath.Join(repo, ".satellites"), 0o755); err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(repo, ".satellites", "satellites.toml")
		if err := os.WriteFile(marker, []byte("# pre-existing\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := ensureScaffoldOnLocalInstall(&out, repo, func(string) bool { return false }); err != nil {
			t.Fatalf("ensureScaffold: %v", err)
		}
		// The pre-existing toml is untouched (init would rewrite it), and there is
		// no .mcp.json because init did not run.
		b, _ := os.ReadFile(marker)
		if string(b) != "# pre-existing\n" {
			t.Errorf("declined re-init must not clobber satellites.toml, got:\n%s", b)
		}
		if _, err := os.Stat(filepath.Join(repo, ".mcp.json")); err == nil {
			t.Errorf("declined re-init must not write .mcp.json")
		}
		if !strings.Contains(out.String(), "already initialized") {
			t.Errorf("expected a steer message, got:\n%s", out.String())
		}
	})
}
