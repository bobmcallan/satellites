package cliconfig

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestSaveLoadCredential_RoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	in := Credential{ServerURL: "https://api.example/", Token: "sk_exec_1", KeyID: "apk_1", Role: "executor"}
	if err := SaveCredential(in); err != nil {
		t.Fatalf("SaveCredential: %v", err)
	}

	// Trailing-slash-insensitive match on lookup.
	got, err := LoadCredential("https://api.example")
	if err != nil {
		t.Fatalf("LoadCredential: %v", err)
	}
	if got.Token != "sk_exec_1" || got.KeyID != "apk_1" || got.Role != "executor" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if got.CreatedAt == "" {
		t.Error("CreatedAt should be stamped on save")
	}

	// File 0600, dir 0700.
	path, _ := CredentialsPath()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %o, want 600", perm)
	}
	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Errorf("dir mode = %o, want 700", perm)
	}
}

func TestSaveCredential_UpsertByServer(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := SaveCredential(Credential{ServerURL: "https://a.example", Token: "t1"}); err != nil {
		t.Fatal(err)
	}
	if err := SaveCredential(Credential{ServerURL: "https://b.example", Token: "t2"}); err != nil {
		t.Fatal(err)
	}
	// Re-save for server a — must replace, not duplicate.
	if err := SaveCredential(Credential{ServerURL: "https://a.example", Token: "t1-new"}); err != nil {
		t.Fatal(err)
	}

	got, err := LoadCredential("https://a.example")
	if err != nil {
		t.Fatal(err)
	}
	if got.Token != "t1-new" {
		t.Errorf("token = %q, want t1-new", got.Token)
	}

	path, _ := CredentialsPath()
	b, _ := os.ReadFile(path)
	var f credentialsFile
	if _, err := toml.Decode(string(b), &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Credential) != 2 {
		t.Errorf("entries = %d, want 2 (no duplicate)", len(f.Credential))
	}
}

func TestLoadCredential_Unknown(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, err := LoadCredential("https://nope.example"); !errors.Is(err, ErrNoCredential) {
		t.Errorf("err = %v, want ErrNoCredential", err)
	}
}

func TestCredentialsPath_XDGAndFallback(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdgtest")
	p, err := CredentialsPath()
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join("/tmp/xdgtest", "satellites", "credentials.toml") {
		t.Errorf("XDG path = %q", p)
	}

	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	p, err = CredentialsPath()
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join(home, ".config", "satellites", "credentials.toml") {
		t.Errorf("fallback path = %q", p)
	}
}
