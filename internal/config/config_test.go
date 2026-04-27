package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestLoad_Happy_DevDefaults(t *testing.T) {
	clearEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.Env != "dev" {
		t.Errorf("Env = %q, want \"dev\"", cfg.Env)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want \"info\"", cfg.LogLevel)
	}
	if !cfg.DevMode {
		t.Errorf("DevMode = false, want true (default dev env)")
	}
	if cfg.DevUsername != "dev@satellites.local" {
		t.Errorf("DevUsername = %q, want \"dev@satellites.local\"", cfg.DevUsername)
	}
	if cfg.DevPassword != "dev123" {
		t.Errorf("DevPassword = %q, want \"dev123\"", cfg.DevPassword)
	}
	if cfg.OAuthRedirectBaseURL != "http://localhost:8080" {
		t.Errorf("OAuthRedirectBaseURL = %q, want \"http://localhost:8080\"", cfg.OAuthRedirectBaseURL)
	}
	if cfg.OAuthTokenCacheTTL != 5*time.Minute {
		t.Errorf("OAuthTokenCacheTTL = %s, want 5m", cfg.OAuthTokenCacheTTL)
	}
	if cfg.DocsDir != "/app/docs" {
		t.Errorf("DocsDir = %q, want \"/app/docs\"", cfg.DocsDir)
	}
	if cfg.GrantsEnforced {
		t.Errorf("GrantsEnforced = true, want false")
	}
}

// TestDescribe_CoversAllFields walks the Config struct via reflection and
// asserts each exported field has a matching entry in Describe(). Trips when
// a new field is added to Config without a describeTable entry.
func TestDescribe_CoversAllFields(t *testing.T) {
	docs := Describe()
	docByField := make(map[string]FieldDoc, len(docs))
	for _, d := range docs {
		docByField[d.Field] = d
	}

	rt := reflect.TypeOf(Config{})
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		if _, ok := docByField[f.Name]; !ok {
			t.Errorf("Config.%s has no Describe() entry — add one to describeTable", f.Name)
		}
	}

	// Reverse direction: every Describe() entry must name a real field.
	for _, d := range docs {
		if _, ok := rt.FieldByName(d.Field); !ok {
			t.Errorf("describeTable references missing field %q", d.Field)
		}
	}
}

// TestDescribe_AllFieldsCovered asserts each Describe() entry has either a
// non-empty Default OR ProdRequired=true. AC2's "default OR prod-required"
// rule is the substantive form of "every field is configured for both
// environments".
func TestDescribe_AllFieldsCovered(t *testing.T) {
	for _, d := range Describe() {
		if d.Default == "" && !d.ProdRequired {
			t.Errorf("Field %q has empty Default and ProdRequired=false — pick one", d.Field)
		}
		if d.Env == "" {
			t.Errorf("Field %q has empty Env", d.Field)
		}
		if d.Description == "" {
			t.Errorf("Field %q has empty Description", d.Field)
		}
	}
}

func TestLoad_OAuthPartialCreds(t *testing.T) {
	cases := []struct {
		name string
		envs map[string]string
	}{
		{"google id only", map[string]string{"GOOGLE_CLIENT_ID": "x"}},
		{"google secret only", map[string]string{"GOOGLE_CLIENT_SECRET": "y"}},
		{"github id only", map[string]string{"GITHUB_CLIENT_ID": "x"}},
		{"github secret only", map[string]string{"GITHUB_CLIENT_SECRET": "y"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tc.envs {
				t.Setenv(k, v)
			}
			if _, err := Load(); err == nil {
				t.Fatalf("Load() = nil, want error on partial OAuth creds")
			}
		})
	}
}

func TestLoad_InvalidLogLevel(t *testing.T) {
	clearEnv(t)
	t.Setenv("LOG_LEVEL", "bogus")
	if _, err := Load(); err == nil {
		t.Fatalf("Load() = nil, want error on invalid LOG_LEVEL")
	}
}

func TestLoad_OAuthCacheTTLOverride(t *testing.T) {
	clearEnv(t)
	t.Setenv("OAUTH_TOKEN_CACHE_TTL", "30s")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if cfg.OAuthTokenCacheTTL != 30*time.Second {
		t.Errorf("OAuthTokenCacheTTL = %s, want 30s", cfg.OAuthTokenCacheTTL)
	}

	clearEnv(t)
	t.Setenv("OAUTH_TOKEN_CACHE_TTL", "garbage")
	if _, err := Load(); err == nil {
		t.Fatalf("Load() = nil, want error on garbage TTL")
	}

	clearEnv(t)
	t.Setenv("OAUTH_TOKEN_CACHE_TTL", "0s")
	if _, err := Load(); err == nil {
		t.Fatalf("Load() = nil, want error on zero TTL (out of range)")
	}
}

func TestLoad_ProdSecretsAreEmpty(t *testing.T) {
	clearEnv(t)
	t.Setenv("ENV", "prod")
	t.Setenv("DB_DSN", "ws://db.internal:8000/rpc")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if cfg.DevUsername != "" {
		t.Errorf("DevUsername = %q in prod, want empty", cfg.DevUsername)
	}
	if cfg.DevPassword != "" {
		t.Errorf("DevPassword = %q in prod, want empty", cfg.DevPassword)
	}
	if cfg.OAuthRedirectBaseURL != "" {
		t.Errorf("OAuthRedirectBaseURL = %q in prod (no override), want empty", cfg.OAuthRedirectBaseURL)
	}
}

func TestLoad_EnvOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("PORT", "9090")
	t.Setenv("ENV", "prod")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("DB_DSN", "ws://db.internal:8000/rpc")
	t.Setenv("FLY_MACHINE_ID", "1234abcd")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if cfg.Port != 9090 {
		t.Errorf("Port = %d, want 9090", cfg.Port)
	}
	if cfg.Env != "prod" {
		t.Errorf("Env = %q, want \"prod\"", cfg.Env)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want \"debug\"", cfg.LogLevel)
	}
	if cfg.DBDSN != "ws://db.internal:8000/rpc" {
		t.Errorf("DBDSN = %q", cfg.DBDSN)
	}
	if cfg.FlyMachineID != "1234abcd" {
		t.Errorf("FlyMachineID = %q", cfg.FlyMachineID)
	}
	if cfg.DevMode {
		t.Errorf("DevMode = true, want false in prod with default DEV_MODE")
	}
}

func TestLoad_MissingDBDSN_InProd(t *testing.T) {
	clearEnv(t)
	t.Setenv("ENV", "prod")
	_, err := Load()
	if err == nil {
		t.Fatalf("Load() = nil, want error about DB_DSN")
	}
}

func TestLoad_InvalidPort(t *testing.T) {
	clearEnv(t)
	t.Setenv("PORT", "abc")
	if _, err := Load(); err == nil {
		t.Fatalf("Load() = nil, want error on non-numeric PORT")
	}

	t.Setenv("PORT", "99999")
	if _, err := Load(); err == nil {
		t.Fatalf("Load() = nil, want error on out-of-range PORT")
	}
}

func TestLoad_InvalidEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("ENV", "staging")
	if _, err := Load(); err == nil {
		t.Fatalf("Load() = nil, want error on unknown ENV")
	}
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"PORT", "SATELLITES_PORT", "ENV", "LOG_LEVEL", "DEV_MODE", "DB_DSN",
		"FLY_MACHINE_ID", "DEV_USERNAME", "DEV_PASSWORD",
		"GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET",
		"GITHUB_CLIENT_ID", "GITHUB_CLIENT_SECRET",
		"OAUTH_REDIRECT_BASE_URL", "OAUTH_TOKEN_CACHE_TTL",
		"SATELLITES_API_KEYS", "DOCS_DIR", "SATELLITES_GRANTS_ENFORCED",
		"SATELLITES_CONFIG",
	} {
		t.Setenv(k, "")
	}
}

// chdirTo cd's the test process into dir for the duration of the test so
// the loader's ./satellites.toml lookup is scoped to a tmpdir, not the
// repo root.
func chdirTo(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

// writeTOML drops the body at <dir>/satellites.toml and returns the path.
func writeTOML(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "satellites.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// TestLoad_NoTOMLNoEnvBootsDefaults asserts AC3 — with no env vars and no
// TOML file, Load() returns a fully-populated Config that passes validate().
func TestLoad_NoTOMLNoEnvBootsDefaults(t *testing.T) {
	clearEnv(t)
	chdirTo(t, t.TempDir())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.Env != "dev" {
		t.Errorf("Env = %q, want dev", cfg.Env)
	}
	if !cfg.DevMode {
		t.Errorf("DevMode = false, want true (no env, no TOML, dev default)")
	}
	if cfg.DocsDir != "/app/docs" {
		t.Errorf("DocsDir = %q, want /app/docs", cfg.DocsDir)
	}
	if cfg.OAuthTokenCacheTTL != 5*time.Minute {
		t.Errorf("OAuthTokenCacheTTL = %s, want 5m", cfg.OAuthTokenCacheTTL)
	}
}

// TestLoad_TOML_File asserts AC1 — Load() reads a TOML file when one is
// present in the cwd and surfaces its values into the returned Config.
func TestLoad_TOML_File(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	chdirTo(t, dir)
	writeTOML(t, dir, `
port = 9091
log_level = "debug"
docs_dir = "/var/satellites/docs"
api_keys = ["alpha", "beta"]
oauth_token_cache_ttl = "10m"
grants_enforced = true
`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if cfg.Port != 9091 {
		t.Errorf("Port = %d, want 9091 (from TOML)", cfg.Port)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want debug (from TOML)", cfg.LogLevel)
	}
	if cfg.DocsDir != "/var/satellites/docs" {
		t.Errorf("DocsDir = %q, want /var/satellites/docs (from TOML)", cfg.DocsDir)
	}
	if !cfg.GrantsEnforced {
		t.Errorf("GrantsEnforced = false, want true (from TOML)")
	}
	if cfg.OAuthTokenCacheTTL != 10*time.Minute {
		t.Errorf("OAuthTokenCacheTTL = %s, want 10m (from TOML)", cfg.OAuthTokenCacheTTL)
	}
	if got, want := cfg.APIKeys, []string{"alpha", "beta"}; !reflect.DeepEqual(got, want) {
		t.Errorf("APIKeys = %v, want %v", got, want)
	}
}

// TestLoad_PrecedenceTOMLOverridesDefault asserts AC2 — when TOML sets a
// field and no env override exists, the TOML value beats the code default.
func TestLoad_PrecedenceTOMLOverridesDefault(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	chdirTo(t, dir)
	writeTOML(t, dir, `port = 7000
docs_dir = "/etc/satellites/docs"
`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if cfg.Port != 7000 {
		t.Errorf("Port = %d, want 7000 (TOML overrides default 8080)", cfg.Port)
	}
	if cfg.DocsDir != "/etc/satellites/docs" {
		t.Errorf("DocsDir = %q, want /etc/satellites/docs (TOML overrides /app/docs)", cfg.DocsDir)
	}
}

// TestLoad_PrecedenceEnvOverridesTOML asserts AC2 — when both env var and
// TOML set the same field, env wins. Covers one int (PORT), one string
// (LOG_LEVEL), one bool (SATELLITES_GRANTS_ENFORCED), and one duration
// (OAUTH_TOKEN_CACHE_TTL).
func TestLoad_PrecedenceEnvOverridesTOML(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	chdirTo(t, dir)
	writeTOML(t, dir, `port = 7000
log_level = "debug"
grants_enforced = false
oauth_token_cache_ttl = "10m"
`)
	t.Setenv("PORT", "9999")
	t.Setenv("LOG_LEVEL", "warn")
	t.Setenv("SATELLITES_GRANTS_ENFORCED", "true")
	t.Setenv("OAUTH_TOKEN_CACHE_TTL", "30s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if cfg.Port != 9999 {
		t.Errorf("Port = %d, want 9999 (env beats TOML 7000)", cfg.Port)
	}
	if cfg.LogLevel != "warn" {
		t.Errorf("LogLevel = %q, want warn (env beats TOML debug)", cfg.LogLevel)
	}
	if !cfg.GrantsEnforced {
		t.Errorf("GrantsEnforced = false, want true (env beats TOML false)")
	}
	if cfg.OAuthTokenCacheTTL != 30*time.Second {
		t.Errorf("OAuthTokenCacheTTL = %s, want 30s (env beats TOML 10m)", cfg.OAuthTokenCacheTTL)
	}
}

// TestLoad_SATELLITES_CONFIG_MissingFile asserts that an explicit
// SATELLITES_CONFIG path that doesn't exist returns an error — operators
// who name the file want it loaded; silent fallback would mask typos.
func TestLoad_SATELLITES_CONFIG_MissingFile(t *testing.T) {
	clearEnv(t)
	chdirTo(t, t.TempDir())
	t.Setenv("SATELLITES_CONFIG", "/nonexistent/satellites.toml")

	if _, err := Load(); err == nil {
		t.Fatalf("Load() = nil, want error on missing SATELLITES_CONFIG path")
	}
}

// TestLoad_SATELLITES_CONFIG_ExplicitPath asserts that an explicit
// SATELLITES_CONFIG path overrides the default ./satellites.toml lookup.
func TestLoad_SATELLITES_CONFIG_ExplicitPath(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	chdirTo(t, dir)
	// Drop a misleading file at the cwd to prove the explicit path wins.
	writeTOML(t, dir, `port = 1111`)
	otherDir := t.TempDir()
	otherPath := filepath.Join(otherDir, "satellites.toml")
	if err := os.WriteFile(otherPath, []byte(`port = 2222`), 0o600); err != nil {
		t.Fatalf("write other toml: %v", err)
	}
	t.Setenv("SATELLITES_CONFIG", otherPath)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if cfg.Port != 2222 {
		t.Errorf("Port = %d, want 2222 (SATELLITES_CONFIG path), got cwd value instead", cfg.Port)
	}
}
