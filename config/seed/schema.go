package seed

import (
	"bytes"
	"context"
	"fmt"
	"sync"

	"gopkg.in/yaml.v3"
)

// InstallSchema is the typed shape of the install-schema artifact's
// YAML frontmatter (config/seed/system/artifacts/satellites_client_install.md).
// Field tags use the YAML key names that appear in the markdown source;
// the satellites_init verb re-marshals into the JSON wire shape.
type InstallSchema struct {
	Name              string             `yaml:"name"`
	Tags              []string           `yaml:"tags"`
	TargetInstallPath string             `yaml:"target_install_path"`
	TargetConfigPath  string             `yaml:"target_config_path"`
	DefaultConfig     DefaultConfig      `yaml:"default_config"`
	AuthBootstrap     AuthBootstrapBlock `yaml:"auth_bootstrap"`
	Install           InstallBlock       `yaml:"install"`
}

// InstallBlock is the templated install-URL block the schema carries.
// Frontmatter templates render against the server's system-variables
// resolver before YAML decoding ({{cli_version}}, {{os}}, {{arch}}).
// satellites_init does not yet read these fields — story 7's MCP
// cutover migrates the verb to source URLs from the schema.
type InstallBlock struct {
	DownloadURL string `yaml:"download_url" json:"download_url,omitempty"`
	SHA256URL   string `yaml:"sha256_url"   json:"sha256_url,omitempty"`
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

// SchemaSource returns the markdown bytes to parse for the install
// schema. The default reads from the embedded artifact; satellites-
// server boot replaces it with a fetcher that pulls from the document
// store and renders templates against the system-variables resolver.
// CLI-local in-process callers (no document store wired) keep the
// embedded fallback so 'satellites init' still works offline.
type SchemaSource func(ctx context.Context) ([]byte, error)

var (
	schemaSourceMu sync.RWMutex
	schemaSourceFn SchemaSource = func(context.Context) ([]byte, error) {
		return ClientInstallMarkdown(), nil
	}
)

// SetClientInstallSchemaSource installs a custom fetcher for the
// install-schema artifact's markdown bytes. Pass nil to restore the
// embedded default.
func SetClientInstallSchemaSource(fn SchemaSource) {
	schemaSourceMu.Lock()
	defer schemaSourceMu.Unlock()
	if fn == nil {
		fn = func(context.Context) ([]byte, error) { return ClientInstallMarkdown(), nil }
	}
	schemaSourceFn = fn
}

// ClientInstallSchema fetches and parses the install schema. With the
// default source it returns the embedded artifact's frontmatter; with
// the document-store source wired by satellites-server boot, it
// returns the latest scope=system document rendered against the
// system-variables resolver.
//
// No caching here: a server-side render depends on per-request system
// variables, and the rendered bytes change between (os, arch, current_
// version) calls. The document store carries the body cache; the
// frontmatter parse is microseconds.
func ClientInstallSchema() (InstallSchema, error) {
	return ClientInstallSchemaCtx(context.Background())
}

// ClientInstallSchemaCtx is the ctx-aware variant. Callers that have a
// request-bound context (with system-variable inputs stamped via
// verb.WithSystemVarContext) should use this to keep the rendered
// install URLs aligned with the caller's OS/arch.
func ClientInstallSchemaCtx(ctx context.Context) (InstallSchema, error) {
	schemaSourceMu.RLock()
	fn := schemaSourceFn
	schemaSourceMu.RUnlock()
	md, err := fn(ctx)
	if err != nil {
		return InstallSchema{}, fmt.Errorf("seed: fetch install schema: %w", err)
	}
	return ParseFrontmatter(md)
}
