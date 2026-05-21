// Package satellitesinit parses the embedded install-schema markdown
// artifact (config/seed/system/artifacts/satellites_client_install.md)
// into a typed schema the satellites_init verb returns on the wire.
//
// The markdown is shipped inside the satellites-server binary via
// //go:embed (see embed.go). Operators edit the frontmatter + rebuild
// to flip a default. Once the document store lands, the same file
// becomes the seed source for a DB-stored row — same struct shape, two
// resolution paths.
package satellitesinit

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// InstallSchema is the typed shape of the artifact's YAML frontmatter.
// Field tags use the YAML key names that appear in the markdown source;
// the satellites_init verb re-marshals into the JSON wire shape.
type InstallSchema struct {
	Name              string             `yaml:"name"`
	Tags              []string           `yaml:"tags"`
	TargetInstallPath string             `yaml:"target_install_path"`
	TargetConfigPath  string             `yaml:"target_config_path"`
	DefaultConfig     DefaultConfig      `yaml:"default_config"`
	AuthBootstrap     AuthBootstrapBlock `yaml:"auth_bootstrap"`
}

// DefaultConfig mirrors the canonical satellites.toml defaults.
type DefaultConfig struct {
	ServerURL      string    `yaml:"server_url"      json:"server_url,omitempty"`
	RepoPath       string    `yaml:"repo_path"       json:"repo_path"`
	WorktreeRoot   string    `yaml:"worktree_root"   json:"worktree_root"`
	LogPath        string    `yaml:"log_path"        json:"log_path"`
	BranchTemplate string    `yaml:"branch_template" json:"branch_template"`
	Auth           AuthBlock `yaml:"auth"            json:"auth"`
}

// AuthBlock mirrors the satellites.toml `[auth]` section. The token
// field is the api-key the CLI presents on every server call. The
// schema ships it empty; Claude fills it from auth_bootstrap.api_key
// after satellites_init mints one.
type AuthBlock struct {
	Token string `yaml:"token" json:"token"`
}

// AuthBootstrapBlock describes the auth bootstrap step the operator
// runs after the binary lands.
type AuthBootstrapBlock struct {
	Kind    string `yaml:"kind"     json:"kind"`
	Command string `yaml:"command"  json:"command,omitempty"`
	EnvHint string `yaml:"env_hint" json:"env_hint,omitempty"`
}

// ParseFrontmatter extracts the YAML frontmatter from a markdown
// document delimited by `---` lines and decodes it into InstallSchema.
// The body (everything after the closing `---`) is ignored — operator
// prose for humans only.
func ParseFrontmatter(md []byte) (InstallSchema, error) {
	const delim = "---"
	trim := bytes.TrimLeft(md, "\n \t\r")
	if !bytes.HasPrefix(trim, []byte(delim)) {
		return InstallSchema{}, fmt.Errorf("satellitesinit: missing opening --- delimiter")
	}
	rest := trim[len(delim):]
	// First newline after the opening delimiter.
	if i := bytes.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[i+1:]
	}
	end := bytes.Index(rest, []byte("\n"+delim))
	if end < 0 {
		return InstallSchema{}, fmt.Errorf("satellitesinit: missing closing --- delimiter")
	}
	fm := rest[:end]
	var schema InstallSchema
	if err := yaml.Unmarshal(fm, &schema); err != nil {
		return InstallSchema{}, fmt.Errorf("satellitesinit: parse frontmatter: %w", err)
	}
	return schema, nil
}
