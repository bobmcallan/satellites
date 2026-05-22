package seed

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// InstallSchema is the typed shape of the install-schema artifact's
// YAML frontmatter. Field tags use the YAML key names from the
// markdown source; the MCP load context drives clients to render the
// template via document_get and parse the rendered body for these
// fields.
type InstallSchema struct {
	Name              string             `yaml:"name"`
	Tags              []string           `yaml:"tags"`
	TargetInstallPath string             `yaml:"target_install_path"`
	TargetConfigPath  string             `yaml:"target_config_path"`
	DefaultConfig     DefaultConfig      `yaml:"default_config"`
	AuthBootstrap     AuthBootstrapBlock `yaml:"auth_bootstrap"`
	Install           InstallBlock       `yaml:"install"`
}

// InstallBlock carries the rendered install descriptor. The server
// substitutes {{cli_version}}, {{os}}, {{arch}} before returning the
// document; the values reaching the consumer are concrete strings.
type InstallBlock struct {
	CLIVersion  string `yaml:"cli_version"  json:"cli_version,omitempty"`
	DownloadURL string `yaml:"download_url" json:"download_url,omitempty"`
	SHA256URL   string `yaml:"sha256_url"   json:"sha256_url,omitempty"`
}

// DefaultConfig mirrors the canonical satellites.toml defaults.
type DefaultConfig struct {
	ServerURL      string    `yaml:"server_url"      json:"server_url,omitempty"`
	ProjectID      string    `yaml:"project_id"      json:"project_id,omitempty"`
	RepoPath       string    `yaml:"repo_path"       json:"repo_path"`
	WorktreeRoot   string    `yaml:"worktree_root"   json:"worktree_root"`
	LogPath        string    `yaml:"log_path"        json:"log_path"`
	BranchTemplate string    `yaml:"branch_template" json:"branch_template"`
	Auth           AuthBlock `yaml:"auth"            json:"auth"`
}

// AuthBlock mirrors the satellites.toml `[auth]` section. The schema
// ships token empty; the auth_bootstrap flow fills it after the
// operator authenticates.
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
		return InstallSchema{}, fmt.Errorf("seed: missing opening --- delimiter")
	}
	rest := trim[len(delim):]
	if i := bytes.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[i+1:]
	}
	end := bytes.Index(rest, []byte("\n"+delim))
	if end < 0 {
		return InstallSchema{}, fmt.Errorf("seed: missing closing --- delimiter")
	}
	fm := rest[:end]
	var schema InstallSchema
	if err := yaml.Unmarshal(fm, &schema); err != nil {
		return InstallSchema{}, fmt.Errorf("seed: parse frontmatter: %w", err)
	}
	return schema, nil
}
