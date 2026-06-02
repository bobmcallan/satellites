// credstore.go owns the user-level credential store — the executor
// api-key the CLI presents on every server call, provisioned by
// `satellites auth`. It deliberately lives OUTSIDE the project tree:
// the secret never sits in a repo-committed `.satellites/satellites.toml`
// (the leak vector behind sty_c2ecbb86). cliconfig.Load resolves the
// token from here, keyed by server_url.
//
// File: $XDG_CONFIG_HOME/satellites/credentials.toml (fallback
// ~/.config/satellites/credentials.toml), dir 0700, file 0600.

package cliconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// ErrNoCredential signals no stored credential matches a server_url.
var ErrNoCredential = errors.New("cliconfig: no credential for server")

// Credential is one stored api-key, keyed by ServerURL. Provisioned by
// `satellites auth`; only ever an executor-role key.
type Credential struct {
	ServerURL string `toml:"server_url"`
	UserID    string `toml:"user_id,omitempty"`
	Token     string `toml:"token"`
	KeyID     string `toml:"key_id,omitempty"`
	Role      string `toml:"role,omitempty"`
	CreatedAt string `toml:"created_at,omitempty"`
}

type credentialsFile struct {
	Credential []Credential `toml:"credential"`
}

// CredentialsPath returns the user-level credentials file path:
// $XDG_CONFIG_HOME/satellites/credentials.toml, falling back to
// ~/.config/satellites/credentials.toml.
func CredentialsPath() (string, error) {
	base := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cliconfig: resolve home dir: %w", err)
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "satellites", "credentials.toml"), nil
}

func normalizeServerURL(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), "/")
}

// LoadCredential returns the stored credential whose server_url matches
// (trailing-slash-insensitive), or ErrNoCredential when none does.
func LoadCredential(serverURL string) (Credential, error) {
	want := normalizeServerURL(serverURL)
	if want == "" {
		return Credential{}, ErrNoCredential
	}
	path, err := CredentialsPath()
	if err != nil {
		return Credential{}, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Credential{}, ErrNoCredential
		}
		return Credential{}, fmt.Errorf("cliconfig: read credentials %s: %w", path, err)
	}
	var f credentialsFile
	if _, err := toml.Decode(string(b), &f); err != nil {
		return Credential{}, fmt.Errorf("cliconfig: parse credentials %s: %w", path, err)
	}
	for _, c := range f.Credential {
		if normalizeServerURL(c.ServerURL) == want {
			return c, nil
		}
	}
	return Credential{}, ErrNoCredential
}

// SaveCredential upserts a credential by server_url and writes the
// credentials file atomically (temp + rename) with 0600 perms, creating
// the parent dir at 0700. It is the sole writer of the credential store.
func SaveCredential(c Credential) error {
	c.ServerURL = normalizeServerURL(c.ServerURL)
	if c.ServerURL == "" {
		return fmt.Errorf("cliconfig: SaveCredential: server_url required")
	}
	if strings.TrimSpace(c.Token) == "" {
		return fmt.Errorf("cliconfig: SaveCredential: token required")
	}
	if strings.TrimSpace(c.CreatedAt) == "" {
		c.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	path, err := CredentialsPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("cliconfig: mkdir %s: %w", dir, err)
	}

	var f credentialsFile
	if b, err := os.ReadFile(path); err == nil {
		if _, err := toml.Decode(string(b), &f); err != nil {
			return fmt.Errorf("cliconfig: parse credentials %s: %w", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cliconfig: read credentials %s: %w", path, err)
	}

	replaced := false
	for i := range f.Credential {
		if normalizeServerURL(f.Credential[i].ServerURL) == c.ServerURL {
			f.Credential[i] = c
			replaced = true
			break
		}
	}
	if !replaced {
		f.Credential = append(f.Credential, c)
	}

	var buf strings.Builder
	if err := toml.NewEncoder(&buf).Encode(f); err != nil {
		return fmt.Errorf("cliconfig: encode credentials: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(buf.String()), 0o600); err != nil {
		return fmt.Errorf("cliconfig: write credentials: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("cliconfig: finalize credentials: %w", err)
	}
	return nil
}
