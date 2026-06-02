package cli

import (
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
	"testing"
)

// fakeReleaseSource serves a release whose binary asset is `payload`,
// with a sha256 that is correct unless corruptSha is set.
func newFakeRelease(t *testing.T, tag string, payload []byte, corruptSha bool) (releaseSource, func()) {
	t.Helper()
	name := fmt.Sprintf("satellites-%s-%s-%s", tag, runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256(payload)
	hexSum := hex.EncodeToString(sum[:])
	if corruptSha {
		hexSum = hex.EncodeToString(make([]byte, 32)) // all-zero, won't match
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/bin", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) })
	mux.HandleFunc("/sha", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", hexSum, name)
	})
	srv := httptest.NewServer(mux)

	src := fakeSource{tag: tag, assets: map[string]string{
		name:             srv.URL + "/bin",
		name + ".sha256": srv.URL + "/sha",
	}}
	return src, srv.Close
}

type fakeSource struct {
	tag    string
	assets map[string]string
}

func (f fakeSource) Latest(ctx context.Context) (string, map[string]string, error) {
	return f.tag, f.assets, nil
}

func TestRunUpdate_ReplacesWhenNewer(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "satellites")
	if err := os.WriteFile(exe, []byte("OLD BINARY"), 0o755); err != nil {
		t.Fatal(err)
	}
	newBytes := []byte("NEW BINARY v0.0.2")
	src, stop := newFakeRelease(t, "v0.0.2", newBytes, false)
	defer stop()

	if err := runUpdate(context.Background(), io.Discard, exe, "v0.0.1", src, false); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	got, _ := os.ReadFile(exe)
	if string(got) != string(newBytes) {
		t.Errorf("binary not replaced: %q", got)
	}
	if fi, _ := os.Stat(exe); fi.Mode().Perm()&0o100 == 0 {
		t.Error("replaced binary is not executable")
	}
}

func TestRunUpdate_NoOpWhenCurrent(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "satellites")
	_ = os.WriteFile(exe, []byte("CURRENT"), 0o755)
	src, stop := newFakeRelease(t, "v0.0.5", []byte("whatever"), false)
	defer stop()

	if err := runUpdate(context.Background(), io.Discard, exe, "v0.0.5", src, false); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "CURRENT" {
		t.Errorf("binary should be untouched when current, got %q", got)
	}
}

func TestRunUpdate_ChecksumMismatchLeavesIntact(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "satellites")
	_ = os.WriteFile(exe, []byte("ORIGINAL"), 0o755)
	src, stop := newFakeRelease(t, "v0.0.2", []byte("tampered"), true)
	defer stop()

	err := runUpdate(context.Background(), io.Discard, exe, "v0.0.1", src, false)
	if err == nil {
		t.Fatal("expected checksum-mismatch error")
	}
	if got, _ := os.ReadFile(exe); string(got) != "ORIGINAL" {
		t.Errorf("binary must be intact on checksum failure, got %q", got)
	}
	// No leftover temp files in the dir.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("expected only the original binary, found %d entries", len(entries))
	}
}

func TestRunUpdate_CheckOnlyDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "satellites")
	_ = os.WriteFile(exe, []byte("ORIGINAL"), 0o755)
	src, stop := newFakeRelease(t, "v0.0.2", []byte("new"), false)
	defer stop()

	if err := runUpdate(context.Background(), io.Discard, exe, "v0.0.1", src, true); err != nil {
		t.Fatalf("runUpdate --check: %v", err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "ORIGINAL" {
		t.Errorf("--check must not write, got %q", got)
	}
}

func TestIsNewer(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
	}{
		{"v0.0.1", "v0.0.2", true},
		{"v0.0.2", "v0.0.2", false},
		{"v0.0.3", "v0.0.2", false},
		{"v0.1.0", "v0.0.9", false},
		{"v0.0.9", "v0.1.0", true},
		{"dev", "v0.0.1", true},    // unparseable current → update
		{"v0.0.1", "weird", false}, // unparseable latest → don't chase
	}
	for _, c := range cases {
		if got := isNewer(c.cur, c.latest); got != c.want {
			t.Errorf("isNewer(%q,%q)=%v want %v", c.cur, c.latest, got, c.want)
		}
	}
}
