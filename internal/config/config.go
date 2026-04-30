// Package config exposes the runtime configuration for satellites-v4
// binaries. Load() resolves config from three layered sources — code
// defaults, an optional TOML file, and process env vars — validates the
// resulting Config for the selected environment, and returns it.
//
// Resolution order (highest to lowest precedence):
//
//  1. Process env var.
//  2. TOML file (path resolved via SATELLITES_CONFIG, else ./satellites.toml).
//  3. Code default (set in defaults()).
//
// Every exported field on Config has a default assigned in defaults() (so
// the binary boots in dev with zero env vars and no TOML file) or an
// explicit prod-required validation in validate(). The single source of
// truth for the (field, env override, default, prod-required) mapping is
// the slice returned by Describe().
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"
)

// Config is the validated runtime configuration for a satellites binary.
//
// Every exported field maps to one TOML key (via the `toml` struct tag)
// and one env var override. Run Describe() for the canonical (field, env,
// default, prod_required) table.
type Config struct {
	// Port is the HTTP listen port. Default 8080.
	// Env override: PORT (also SATELLITES_PORT).
	Port int `toml:"port"`

	// Env is the deployment environment. Canonical values: "dev" or "prod".
	// Env override: ENV. Default "dev".
	Env string `toml:"env"`

	// LogLevel is the arbor log level ("trace", "debug", "info", "warn",
	// "error"). Env override: LOG_LEVEL. Default "info".
	LogLevel string `toml:"log_level"`

	// DevMode relaxes production-only gates (required secrets, strict CORS)
	// and turns on the dev-mode quick-signin and DEV portal affordances.
	// Defaults true when Env=="dev". Env override: DEV_MODE.
	DevMode bool `toml:"dev_mode"`

	// DBDSN is the SurrealDB connection string. Required when Env=="prod"; in
	// dev an empty value boots the server without DB-backed verbs.
	// Env override: DB_DSN.
	DBDSN string `toml:"db_dsn"`

	// FlyMachineID is injected by Fly.io at container start. Empty off-Fly.
	// Env override: FLY_MACHINE_ID.
	FlyMachineID string `toml:"fly_machine_id"`

	// DevUsername is the fixed credential allowed when DevMode is active. In
	// dev defaults to "dev@satellites.local" so the dev-mode quick-signin
	// works without any env vars. Empty in prod. Env override: DEV_USERNAME.
	DevUsername string `toml:"dev_username"`

	// DevPassword is the fixed DevMode password. In dev defaults to "dev123".
	// Never logged. Empty in prod. Env override: DEV_PASSWORD.
	DevPassword string `toml:"dev_password"`

	// GoogleClientID is the OAuth 2.0 client id. Env override: GOOGLE_CLIENT_ID.
	// Empty disables the provider. Must be paired with GoogleClientSecret —
	// half a credential is rejected by validate().
	GoogleClientID string `toml:"google_client_id"`

	// GoogleClientSecret is the OAuth 2.0 client secret. Never logged.
	// Env override: GOOGLE_CLIENT_SECRET.
	GoogleClientSecret string `toml:"google_client_secret"`

	// GithubClientID is the OAuth 2.0 client id for the GitHub provider.
	// Env override: GITHUB_CLIENT_ID. Empty disables the provider; must be
	// paired with GithubClientSecret.
	GithubClientID string `toml:"github_client_id"`

	// GithubClientSecret is the GitHub OAuth client secret. Never logged.
	// Env override: GITHUB_CLIENT_SECRET.
	GithubClientSecret string `toml:"github_client_secret"`

	// OAuthRedirectBaseURL is the absolute base URL the auth handlers append
	// the per-provider callback path to. In dev defaults to
	// "http://localhost:<Port>" so OAuth works on `go run` with zero env
	// vars. Required (non-empty) in prod when any provider is configured.
	// Env override: OAUTH_REDIRECT_BASE_URL.
	OAuthRedirectBaseURL string `toml:"oauth_redirect_base_url"`

	// OAuthTokenCacheTTL is how long the MCP-side OAuth token validator
	// caches a successful provider lookup. Default 5m. Range 1s..1h.
	// Env override: OAUTH_TOKEN_CACHE_TTL (Go duration: "5m", "30s", "1h").
	OAuthTokenCacheTTL time.Duration `toml:"oauth_token_cache_ttl"`

	// APIKeys are Bearer tokens accepted on /mcp when a session cookie is
	// absent. Typical use: CI agents + the local Claude harness. In TOML use
	// a native array (api_keys = ["k1","k2"]); in env use the comma-separated
	// form. Empty disables Bearer-API-key auth.
	// Env override: SATELLITES_API_KEYS (comma-separated).
	APIKeys []string `toml:"api_keys"`

	// DocsDir is the container-side path containing the mounted docs
	// volume that document_ingest_file reads from. Defaults to /app/docs.
	// Env override: DOCS_DIR.
	DocsDir string `toml:"docs_dir"`

	// GrantsEnforced toggles the mcpserver grant middleware's enforcement
	// mode. When false (the current default), the middleware is a
	// pass-through. When true, MCP verbs outside the bootstrap allowlist
	// are rejected unless the caller holds a role-grant whose effective
	// verb allowlist covers the tool. Env override: SATELLITES_GRANTS_ENFORCED.
	GrantsEnforced bool `toml:"grants_enforced"`

	// loadedTOMLPath is set by Load() to the absolute path of the TOML
	// file that was actually read (empty when defaults+env supplied the
	// whole config). Read via the LoadedTOMLPath() accessor — the field
	// is unexported so it doesn't show up in Describe()/TOML serialisation.
	loadedTOMLPath string
}

// LoadedTOMLPath returns the absolute path of the TOML file that the
// last Load() call actually read, or "" when the config came from
// defaults+env alone. Used at boot to log proof that the operator's
// TOML file was picked up; tests that mount a TOML at /app/satellites.toml
// assert on this in container.Logs to verify the loader path actually ran.
func (c *Config) LoadedTOMLPath() string {
	return c.loadedTOMLPath
}

// FieldDoc is one entry in the Describe() table — the single source of truth
// for the (config field, env var, default, prod-required) mapping.
type FieldDoc struct {
	// Field is the exported Go field name on Config.
	Field string
	// Env is the env var name read by Load() as an override on top of TOML
	// values and code defaults.
	Env string
	// Default is a human-readable rendering of the dev-mode default.
	Default string
	// ProdRequired is true when validate() rejects the empty value in prod.
	ProdRequired bool
	// Description is a one-line summary suitable for an --env-help dump.
	Description string
}

// describeTable is the canonical Config field documentation. Adding a new
// exported field to Config without adding an entry here trips the
// reflection-based doc-coverage test in config_test.go.
var describeTable = []FieldDoc{
	{Field: "Port", Env: "PORT", Default: "8080", Description: "HTTP listen port (1..65535). SATELLITES_PORT also accepted."},
	{Field: "Env", Env: "ENV", Default: "dev", Description: "Deployment environment: dev or prod."},
	{Field: "LogLevel", Env: "LOG_LEVEL", Default: "info", Description: "Arbor log level: trace, debug, info, warn, error."},
	{Field: "DevMode", Env: "DEV_MODE", Default: "true (when ENV=dev) / false (when ENV=prod)", Description: "Enables dev-mode quick-signin and DEV portal affordances."},
	{Field: "DBDSN", Env: "DB_DSN", Default: "(empty in dev — DB-backed verbs disabled)", ProdRequired: true, Description: "SurrealDB connection string. Required in prod."},
	{Field: "FlyMachineID", Env: "FLY_MACHINE_ID", Default: "(injected by Fly.io at container start; empty off-Fly)", Description: "Fly.io machine identifier; passes through to /healthz and logs."},
	{Field: "DevUsername", Env: "DEV_USERNAME", Default: "dev@satellites.local (dev) / (empty) (prod)", Description: "Fixed credential username for DevMode signin."},
	{Field: "DevPassword", Env: "DEV_PASSWORD", Default: "dev123 (dev) / (empty) (prod)", Description: "Fixed credential password for DevMode signin. Never logged."},
	{Field: "GoogleClientID", Env: "GOOGLE_CLIENT_ID", Default: "(empty — provider disabled)", Description: "OAuth 2.0 client id for Google. Pair with GOOGLE_CLIENT_SECRET."},
	{Field: "GoogleClientSecret", Env: "GOOGLE_CLIENT_SECRET", Default: "(empty — provider disabled)", Description: "OAuth 2.0 client secret for Google. Never logged."},
	{Field: "GithubClientID", Env: "GITHUB_CLIENT_ID", Default: "(empty — provider disabled)", Description: "OAuth 2.0 client id for GitHub. Pair with GITHUB_CLIENT_SECRET."},
	{Field: "GithubClientSecret", Env: "GITHUB_CLIENT_SECRET", Default: "(empty — provider disabled)", Description: "OAuth 2.0 client secret for GitHub. Never logged."},
	{Field: "OAuthRedirectBaseURL", Env: "OAUTH_REDIRECT_BASE_URL", Default: "http://localhost:<Port> (dev) / (empty) (prod)", Description: "Base URL for OAuth callback redirects. Required in prod when any provider is configured."},
	{Field: "OAuthTokenCacheTTL", Env: "OAUTH_TOKEN_CACHE_TTL", Default: "5m", Description: "How long the MCP-side OAuth token validator caches a successful provider lookup."},
	{Field: "APIKeys", Env: "SATELLITES_API_KEYS", Default: "(empty — Bearer-API-key auth disabled)", Description: "Bearer tokens accepted on /mcp. TOML: native array. Env: comma-separated."},
	{Field: "DocsDir", Env: "DOCS_DIR", Default: "/app/docs", Description: "Container-side docs volume path read by document_ingest_file."},
	{Field: "GrantsEnforced", Env: "SATELLITES_GRANTS_ENFORCED", Default: "false", Description: "When true, MCP verbs outside the bootstrap allowlist require a covering role-grant."},
}

// Describe returns the canonical (Field, Env, Default, ProdRequired,
// Description) table for the Config type. Use this in admin / --env-help
// surfaces; do not duplicate the mapping elsewhere.
func Describe() []FieldDoc {
	out := make([]FieldDoc, len(describeTable))
	copy(out, describeTable)
	return out
}

// validLogLevels mirrors the level strings accepted by internal/arbor.New.
var validLogLevels = map[string]struct{}{
	"trace": {}, "debug": {}, "info": {}, "warn": {}, "error": {},
}

// configPathEnv names the env var operators set to point at an explicit
// TOML file. When set and unreadable, Load() fails — explicit asks must
// resolve. Unset falls back to ./satellites.toml (silent if absent).
const configPathEnv = "SATELLITES_CONFIG"

// defaultConfigFile is the file the loader checks when SATELLITES_CONFIG
// is unset. Absence is silent — operators can run on env+defaults alone.
const defaultConfigFile = "satellites.toml"

// Load resolves Config from defaults → TOML → env, validates, and returns.
// Missing required fields (e.g. DB_DSN in prod) return a structured error;
// callers should log via arbor and exit non-zero.
func Load() (*Config, error) {
	cfg := defaults()

	overlay, tomlPath, err := readTOMLOverlay()
	if err != nil {
		return nil, err
	}
	cfg.loadedTOMLPath = tomlPath
	envSetDevMode := overlay.applyTo(cfg)

	// Env / DevMode resolution must settle before the dev-mode default
	// block so the right shape applies.
	if v := os.Getenv("ENV"); v != "" {
		cfg.Env = normaliseEnv(v)
	}
	if v := os.Getenv("DEV_MODE"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("invalid DEV_MODE %q: %w", v, err)
		}
		cfg.DevMode = b
		envSetDevMode = true
	}
	if !envSetDevMode {
		cfg.DevMode = cfg.Env == "dev"
	}

	// Dev-mode-only quick-signin defaults: only apply when DevMode is
	// active AND neither TOML nor env supplied a value. Production sets
	// neither default to keep secrets out of binaries.
	if cfg.DevMode {
		if cfg.DevUsername == "" {
			cfg.DevUsername = "dev@satellites.local"
		}
		if cfg.DevPassword == "" {
			cfg.DevPassword = "dev123"
		}
		if cfg.OAuthRedirectBaseURL == "" {
			cfg.OAuthRedirectBaseURL = fmt.Sprintf("http://localhost:%d", cfg.Port)
		}
	}

	if err := applyEnvOverrides(cfg); err != nil {
		return nil, err
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// duration wraps time.Duration with a TextUnmarshaler so go-toml/v2 can
// decode Go-duration strings ("5m", "30s", "1h") into the OAuth TTL field.
type duration time.Duration

// UnmarshalText parses a Go duration string into the wrapper.
func (d *duration) UnmarshalText(text []byte) error {
	parsed, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	*d = duration(parsed)
	return nil
}

// tomlOverlay is the TOML-decoding shadow of Config. Pointer fields let
// the loader distinguish "TOML set this" (non-nil) from "TOML didn't
// mention it" (nil), so the overlay can override defaults selectively
// without overwriting unrelated fields. Duration is wrapped to satisfy
// go-toml/v2, which lacks built-in time.Duration support.
type tomlOverlay struct {
	Port                 *int      `toml:"port"`
	Env                  *string   `toml:"env"`
	LogLevel             *string   `toml:"log_level"`
	DevMode              *bool     `toml:"dev_mode"`
	DBDSN                *string   `toml:"db_dsn"`
	FlyMachineID         *string   `toml:"fly_machine_id"`
	DevUsername          *string   `toml:"dev_username"`
	DevPassword          *string   `toml:"dev_password"`
	GoogleClientID       *string   `toml:"google_client_id"`
	GoogleClientSecret   *string   `toml:"google_client_secret"`
	GithubClientID       *string   `toml:"github_client_id"`
	GithubClientSecret   *string   `toml:"github_client_secret"`
	OAuthRedirectBaseURL *string   `toml:"oauth_redirect_base_url"`
	OAuthTokenCacheTTL   *duration `toml:"oauth_token_cache_ttl"`
	APIKeys              []string  `toml:"api_keys"`
	DocsDir              *string   `toml:"docs_dir"`
	GrantsEnforced       *bool     `toml:"grants_enforced"`
}

// applyTo copies overlay values into cfg for every non-nil field. Returns
// true when the overlay supplied a DevMode value so the caller can skip
// the env-derived default.
func (o tomlOverlay) applyTo(cfg *Config) bool {
	if o.Port != nil {
		cfg.Port = *o.Port
	}
	if o.Env != nil {
		cfg.Env = normaliseEnv(*o.Env)
	}
	if o.LogLevel != nil {
		cfg.LogLevel = strings.ToLower(*o.LogLevel)
	}
	devModeSet := false
	if o.DevMode != nil {
		cfg.DevMode = *o.DevMode
		devModeSet = true
	}
	if o.DBDSN != nil {
		cfg.DBDSN = *o.DBDSN
	}
	if o.FlyMachineID != nil {
		cfg.FlyMachineID = *o.FlyMachineID
	}
	if o.DevUsername != nil {
		cfg.DevUsername = *o.DevUsername
	}
	if o.DevPassword != nil {
		cfg.DevPassword = *o.DevPassword
	}
	if o.GoogleClientID != nil {
		cfg.GoogleClientID = *o.GoogleClientID
	}
	if o.GoogleClientSecret != nil {
		cfg.GoogleClientSecret = *o.GoogleClientSecret
	}
	if o.GithubClientID != nil {
		cfg.GithubClientID = *o.GithubClientID
	}
	if o.GithubClientSecret != nil {
		cfg.GithubClientSecret = *o.GithubClientSecret
	}
	if o.OAuthRedirectBaseURL != nil {
		cfg.OAuthRedirectBaseURL = *o.OAuthRedirectBaseURL
	}
	if o.OAuthTokenCacheTTL != nil {
		cfg.OAuthTokenCacheTTL = time.Duration(*o.OAuthTokenCacheTTL)
	}
	if o.APIKeys != nil {
		cfg.APIKeys = append([]string(nil), o.APIKeys...)
	}
	if o.DocsDir != nil {
		cfg.DocsDir = *o.DocsDir
	}
	if o.GrantsEnforced != nil {
		cfg.GrantsEnforced = *o.GrantsEnforced
	}
	return devModeSet
}

// readTOMLOverlay resolves the TOML path, parses the file if present, and
// returns the overlay alongside the path that was actually read. An empty
// path means no TOML was loaded (the silent-when-absent default-config
// case); callers stamp this onto Config.loadedTOMLPath so the boot log
// can prove the loader ran.
func readTOMLOverlay() (tomlOverlay, string, error) {
	var overlay tomlOverlay
	path, explicit, err := resolveConfigPath()
	if err != nil {
		return overlay, "", err
	}
	if path == "" {
		return overlay, "", nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if !explicit && os.IsNotExist(err) {
			return overlay, "", nil
		}
		return overlay, "", fmt.Errorf("read config %s: %w", path, err)
	}
	if err := toml.Unmarshal(raw, &overlay); err != nil {
		return overlay, "", fmt.Errorf("parse config %s: %w", path, err)
	}
	return overlay, path, nil
}

// defaults builds the in-code default Config. This is the lowest-precedence
// layer; TOML and env vars override it.
func defaults() *Config {
	return &Config{
		Port:               8080,
		Env:                "dev",
		LogLevel:           "info",
		DevMode:            true,
		DBDSN:              "",
		FlyMachineID:       "",
		DocsDir:            "/app/docs",
		OAuthTokenCacheTTL: 5 * time.Minute,
	}
}

// resolveConfigPath returns (path, explicit, err). When SATELLITES_CONFIG
// is set, the path is explicit and a missing file is an error. Otherwise
// the loader looks for ./satellites.toml; an absent default file returns
// path="" with no error.
func resolveConfigPath() (string, bool, error) {
	if p := strings.TrimSpace(os.Getenv(configPathEnv)); p != "" {
		return p, true, nil
	}
	if _, err := os.Stat(defaultConfigFile); err == nil {
		return defaultConfigFile, false, nil
	}
	return "", false, nil
}

// applyEnvOverrides mutates cfg in place with values from the process env.
// Each override is gated on a non-empty env var; empty (or unset) leaves
// the prior value intact.
func applyEnvOverrides(cfg *Config) error {
	if v := firstNonEmpty(os.Getenv("PORT"), os.Getenv("SATELLITES_PORT")); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("invalid PORT %q: %w", v, err)
		}
		cfg.Port = p
	}
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		cfg.LogLevel = strings.ToLower(v)
	}
	if v := os.Getenv("DB_DSN"); v != "" {
		cfg.DBDSN = v
	}
	if v := os.Getenv("FLY_MACHINE_ID"); v != "" {
		cfg.FlyMachineID = v
	}
	if v := os.Getenv("DEV_USERNAME"); v != "" {
		cfg.DevUsername = v
	}
	if v := os.Getenv("DEV_PASSWORD"); v != "" {
		cfg.DevPassword = v
	}
	if v := os.Getenv("GOOGLE_CLIENT_ID"); v != "" {
		cfg.GoogleClientID = v
	}
	if v := os.Getenv("GOOGLE_CLIENT_SECRET"); v != "" {
		cfg.GoogleClientSecret = v
	}
	if v := os.Getenv("GITHUB_CLIENT_ID"); v != "" {
		cfg.GithubClientID = v
	}
	if v := os.Getenv("GITHUB_CLIENT_SECRET"); v != "" {
		cfg.GithubClientSecret = v
	}
	if v := os.Getenv("OAUTH_REDIRECT_BASE_URL"); v != "" {
		cfg.OAuthRedirectBaseURL = v
	}
	if v := os.Getenv("OAUTH_TOKEN_CACHE_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("invalid OAUTH_TOKEN_CACHE_TTL %q: %w", v, err)
		}
		cfg.OAuthTokenCacheTTL = d
	}
	if v := os.Getenv("SATELLITES_API_KEYS"); v != "" {
		var keys []string
		for _, part := range strings.Split(v, ",") {
			if k := strings.TrimSpace(part); k != "" {
				keys = append(keys, k)
			}
		}
		cfg.APIKeys = keys
	}
	if v := os.Getenv("DOCS_DIR"); v != "" {
		cfg.DocsDir = v
	}
	if v := os.Getenv("SATELLITES_GRANTS_ENFORCED"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("invalid SATELLITES_GRANTS_ENFORCED %q: %w", v, err)
		}
		cfg.GrantsEnforced = b
	}
	return nil
}

func (c *Config) validate() error {
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("port out of range: %d (must be 1..65535)", c.Port)
	}
	if c.Env != "dev" && c.Env != "prod" {
		return fmt.Errorf("invalid ENV %q (must be dev or prod)", c.Env)
	}
	if _, ok := validLogLevels[c.LogLevel]; !ok {
		return fmt.Errorf("invalid LOG_LEVEL %q (must be trace, debug, info, warn, or error)", c.LogLevel)
	}
	if c.OAuthTokenCacheTTL < time.Second || c.OAuthTokenCacheTTL > time.Hour {
		return fmt.Errorf("OAUTH_TOKEN_CACHE_TTL out of range: %s (must be 1s..1h)", c.OAuthTokenCacheTTL)
	}
	if c.Env == "prod" && strings.TrimSpace(c.DBDSN) == "" {
		return fmt.Errorf("DB_DSN is required when ENV=prod")
	}
	if (c.GoogleClientID == "") != (c.GoogleClientSecret == "") {
		return fmt.Errorf("GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET must be set together (got id=%t, secret=%t)", c.GoogleClientID != "", c.GoogleClientSecret != "")
	}
	if (c.GithubClientID == "") != (c.GithubClientSecret == "") {
		return fmt.Errorf("GITHUB_CLIENT_ID and GITHUB_CLIENT_SECRET must be set together (got id=%t, secret=%t)", c.GithubClientID != "", c.GithubClientSecret != "")
	}
	hasOAuth := c.GoogleClientID != "" || c.GithubClientID != ""
	if c.Env == "prod" && hasOAuth && strings.TrimSpace(c.OAuthRedirectBaseURL) == "" {
		return fmt.Errorf("OAUTH_REDIRECT_BASE_URL is required when ENV=prod and any OAuth provider is configured")
	}
	return nil
}

func normaliseEnv(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "dev", "development":
		return "dev"
	case "prod", "production":
		return "prod"
	default:
		return strings.ToLower(strings.TrimSpace(v))
	}
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
