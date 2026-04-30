package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLoad_Happy_ProdDefaults(t *testing.T) {
	clearEnv(t)
	chdirTo(t, t.TempDir())
	cfg, _ := Load()
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.Env != "prod" {
		t.Errorf("Env = %q, want \"prod\"", cfg.Env)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want \"info\"", cfg.LogLevel)
	}
	if cfg.DevMode {
		t.Errorf("DevMode = true, want false (default prod env)")
	}
	if cfg.DevUsername != "" {
		t.Errorf("DevUsername = %q in prod default, want empty", cfg.DevUsername)
	}
	if cfg.DevPassword != "" {
		t.Errorf("DevPassword = %q in prod default, want empty", cfg.DevPassword)
	}
	if cfg.OAuthRedirectBaseURL != "" {
		t.Errorf("OAuthRedirectBaseURL = %q in prod default, want empty", cfg.OAuthRedirectBaseURL)
	}
	if cfg.OAuthTokenCacheTTL != 4*time.Hour {
		t.Errorf("OAuthTokenCacheTTL = %s, want 4h", cfg.OAuthTokenCacheTTL)
	}
	if cfg.DocsDir != "/app/docs" {
		t.Errorf("DocsDir = %q, want \"/app/docs\"", cfg.DocsDir)
	}
	if cfg.GrantsEnforced {
		t.Errorf("GrantsEnforced = true, want false")
	}
	if cfg.GeminiReviewModel != "gemini-2.5-flash" {
		t.Errorf("GeminiReviewModel = %q, want \"gemini-2.5-flash\"", cfg.GeminiReviewModel)
	}
	if cfg.EmbeddingsProvider != "none" {
		t.Errorf("EmbeddingsProvider = %q, want \"none\"", cfg.EmbeddingsProvider)
	}
}

func TestLoad_DevModeDefaults(t *testing.T) {
	clearEnv(t)
	chdirTo(t, t.TempDir())
	t.Setenv("SATELLITES_ENV", "dev")
	cfg, _ := Load()
	if cfg.Env != "dev" {
		t.Errorf("Env = %q, want \"dev\"", cfg.Env)
	}
	if !cfg.DevMode {
		t.Errorf("DevMode = false, want true (default in dev env)")
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

// TestDescribe_AllFieldsCovered asserts each Describe() entry has a
// non-empty Default — the warn-not-fatal contract requires every field
// to fall back to a code-defined default. Env name and Description must
// also be present.
func TestDescribe_AllFieldsCovered(t *testing.T) {
	for _, d := range Describe() {
		if d.Default == "" {
			t.Errorf("Field %q has empty Default — every field must declare a default", d.Field)
		}
		if d.Env == "" {
			t.Errorf("Field %q has empty Env", d.Field)
		}
		if !strings.HasPrefix(d.Env, "SATELLITES_") {
			t.Errorf("Field %q env %q must be SATELLITES_-prefixed", d.Field, d.Env)
		}
		if d.Description == "" {
			t.Errorf("Field %q has empty Description", d.Field)
		}
	}
}

func TestLoad_OAuthPartialCreds_DegradesToWarning(t *testing.T) {
	cases := []struct {
		name      string
		envs      map[string]string
		wantField string
	}{
		{"google id only", map[string]string{"SATELLITES_GOOGLE_CLIENT_ID": "x"}, "Google"},
		{"google secret only", map[string]string{"SATELLITES_GOOGLE_CLIENT_SECRET": "y"}, "Google"},
		{"github id only", map[string]string{"SATELLITES_GITHUB_CLIENT_ID": "x"}, "GitHub"},
		{"github secret only", map[string]string{"SATELLITES_GITHUB_CLIENT_SECRET": "y"}, "GitHub"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			chdirTo(t, t.TempDir())
			for k, v := range tc.envs {
				t.Setenv(k, v)
			}
			cfg, warnings := Load()
			if cfg == nil {
				t.Fatalf("Load() cfg = nil, want non-nil (boot must always succeed)")
			}
			if !containsContaining(warnings, tc.wantField) || !containsContaining(warnings, "half-set") {
				t.Errorf("warnings missing %s half-set notice; got %v", tc.wantField, warnings)
			}
			// Provider must be disabled (both id and secret cleared).
			if cfg.GoogleClientID != "" || cfg.GoogleClientSecret != "" {
				t.Errorf("Google creds not cleared: id=%q secret=%q", cfg.GoogleClientID, cfg.GoogleClientSecret)
			}
			if cfg.GithubClientID != "" || cfg.GithubClientSecret != "" {
				t.Errorf("GitHub creds not cleared: id=%q secret=%q", cfg.GithubClientID, cfg.GithubClientSecret)
			}
		})
	}
}

func TestLoad_InvalidLogLevel_DegradesToDefault(t *testing.T) {
	clearEnv(t)
	chdirTo(t, t.TempDir())
	t.Setenv("SATELLITES_LOG_LEVEL", "bogus")
	cfg, warnings := Load()
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want \"info\" fallback after bogus override", cfg.LogLevel)
	}
	if !containsContaining(warnings, "SATELLITES_LOG_LEVEL") {
		t.Errorf("warnings missing SATELLITES_LOG_LEVEL notice; got %v", warnings)
	}
}

func TestLoad_OAuthCacheTTLOverride(t *testing.T) {
	clearEnv(t)
	chdirTo(t, t.TempDir())
	t.Setenv("SATELLITES_OAUTH_TOKEN_CACHE_TTL", "30s")
	cfg, _ := Load()
	if cfg.OAuthTokenCacheTTL != 30*time.Second {
		t.Errorf("OAuthTokenCacheTTL = %s, want 30s", cfg.OAuthTokenCacheTTL)
	}

	clearEnv(t)
	t.Setenv("SATELLITES_OAUTH_TOKEN_CACHE_TTL", "garbage")
	cfg, warnings := Load()
	if cfg.OAuthTokenCacheTTL != 4*time.Hour {
		t.Errorf("OAuthTokenCacheTTL = %s, want 4h fallback after garbage override", cfg.OAuthTokenCacheTTL)
	}
	if !containsContaining(warnings, "SATELLITES_OAUTH_TOKEN_CACHE_TTL") {
		t.Errorf("warnings missing TTL notice; got %v", warnings)
	}

	clearEnv(t)
	t.Setenv("SATELLITES_OAUTH_TOKEN_CACHE_TTL", "0s")
	cfg, warnings = Load()
	if cfg.OAuthTokenCacheTTL != 4*time.Hour {
		t.Errorf("OAuthTokenCacheTTL = %s, want 4h fallback after 0s override", cfg.OAuthTokenCacheTTL)
	}
	if !containsContaining(warnings, "SATELLITES_OAUTH_TOKEN_CACHE_TTL") {
		t.Errorf("warnings missing TTL range notice; got %v", warnings)
	}
}

func TestLoad_ProdSecretsAreEmpty(t *testing.T) {
	clearEnv(t)
	chdirTo(t, t.TempDir())
	t.Setenv("SATELLITES_ENV", "prod")
	t.Setenv("SATELLITES_DB_DSN", "ws://db.internal:8000/rpc")
	cfg, _ := Load()
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
	chdirTo(t, t.TempDir())
	t.Setenv("SATELLITES_PORT", "9090")
	t.Setenv("SATELLITES_ENV", "prod")
	t.Setenv("SATELLITES_LOG_LEVEL", "debug")
	t.Setenv("SATELLITES_DB_DSN", "ws://db.internal:8000/rpc")

	cfg, _ := Load()
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
	if cfg.DevMode {
		t.Errorf("DevMode = true, want false in prod with default SATELLITES_DEV_MODE")
	}
}

func TestLoad_MissingDBDSN_InProd_Warns(t *testing.T) {
	clearEnv(t)
	chdirTo(t, t.TempDir())
	t.Setenv("SATELLITES_ENV", "prod")
	cfg, warnings := Load()
	if cfg == nil {
		t.Fatalf("Load() cfg = nil, want non-nil (boot must always succeed)")
	}
	if cfg.DBDSN != "" {
		t.Errorf("DBDSN = %q, want empty default", cfg.DBDSN)
	}
	if !containsContaining(warnings, "SATELLITES_DB_DSN empty") {
		t.Errorf("warnings missing DB_DSN notice; got %v", warnings)
	}
}

func TestLoad_MissingAPIKeys_InProd_Warns(t *testing.T) {
	clearEnv(t)
	chdirTo(t, t.TempDir())
	t.Setenv("SATELLITES_ENV", "prod")
	_, warnings := Load()
	if !containsContaining(warnings, "SATELLITES_API_KEYS empty") {
		t.Errorf("warnings missing API_KEYS notice; got %v", warnings)
	}
}

func TestLoad_InvalidPort_DegradesToDefault(t *testing.T) {
	clearEnv(t)
	chdirTo(t, t.TempDir())
	t.Setenv("SATELLITES_PORT", "abc")
	cfg, warnings := Load()
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080 fallback after non-numeric override", cfg.Port)
	}
	if !containsContaining(warnings, "SATELLITES_PORT") {
		t.Errorf("warnings missing PORT parse notice; got %v", warnings)
	}

	clearEnv(t)
	t.Setenv("SATELLITES_PORT", "99999")
	cfg, warnings = Load()
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080 fallback after out-of-range override", cfg.Port)
	}
	if !containsContaining(warnings, "SATELLITES_PORT") {
		t.Errorf("warnings missing PORT range notice; got %v", warnings)
	}
}

func TestLoad_InvalidEnv_DegradesToDefault(t *testing.T) {
	clearEnv(t)
	chdirTo(t, t.TempDir())
	t.Setenv("SATELLITES_ENV", "staging")
	cfg, warnings := Load()
	if cfg.Env != "prod" {
		t.Errorf("Env = %q, want \"prod\" fallback after unknown env", cfg.Env)
	}
	if !containsContaining(warnings, "SATELLITES_ENV") {
		t.Errorf("warnings missing ENV notice; got %v", warnings)
	}
}

func TestLoad_InvalidDevMode_DegradesToDefault(t *testing.T) {
	clearEnv(t)
	chdirTo(t, t.TempDir())
	t.Setenv("SATELLITES_DEV_MODE", "maybe")
	cfg, warnings := Load()
	if cfg.DevMode {
		t.Errorf("DevMode = true, want false fallback (prod default) after garbage override")
	}
	if !containsContaining(warnings, "SATELLITES_DEV_MODE") {
		t.Errorf("warnings missing DEV_MODE notice; got %v", warnings)
	}
}

// containsContaining returns true when any element in xs contains substr.
func containsContaining(xs []string, substr string) bool {
	for _, x := range xs {
		if strings.Contains(x, substr) {
			return true
		}
	}
	return false
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"SATELLITES_PORT", "SATELLITES_ENV", "SATELLITES_LOG_LEVEL",
		"SATELLITES_DEV_MODE", "SATELLITES_DB_DSN",
		"SATELLITES_DEV_USERNAME", "SATELLITES_DEV_PASSWORD",
		"SATELLITES_GOOGLE_CLIENT_ID", "SATELLITES_GOOGLE_CLIENT_SECRET",
		"SATELLITES_GITHUB_CLIENT_ID", "SATELLITES_GITHUB_CLIENT_SECRET",
		"SATELLITES_OAUTH_REDIRECT_BASE_URL", "SATELLITES_OAUTH_TOKEN_CACHE_TTL",
		"SATELLITES_API_KEYS", "SATELLITES_DOCS_DIR", "SATELLITES_GRANTS_ENFORCED",
		"SATELLITES_GEMINI_API_KEY", "SATELLITES_GEMINI_REVIEW_MODEL",
		"SATELLITES_EMBEDDINGS_PROVIDER", "SATELLITES_EMBEDDINGS_MODEL",
		"SATELLITES_EMBEDDINGS_API_KEY", "SATELLITES_EMBEDDINGS_BASE_URL",
		"SATELLITES_EMBEDDINGS_DIMENSION",
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

// TestLoad_NoTOMLNoEnvBootsDefaults asserts that with no env vars and no
// TOML file, Load() returns a fully-populated Config (the warn-not-fatal
// contract guarantees boot always succeeds).
func TestLoad_NoTOMLNoEnvBootsDefaults(t *testing.T) {
	clearEnv(t)
	chdirTo(t, t.TempDir())

	cfg, _ := Load()
	if cfg == nil {
		t.Fatalf("Load() cfg = nil, want non-nil")
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.Env != "prod" {
		t.Errorf("Env = %q, want prod", cfg.Env)
	}
	if cfg.DevMode {
		t.Errorf("DevMode = true, want false (prod default)")
	}
	if cfg.DocsDir != "/app/docs" {
		t.Errorf("DocsDir = %q, want /app/docs", cfg.DocsDir)
	}
	if cfg.OAuthTokenCacheTTL != 4*time.Hour {
		t.Errorf("OAuthTokenCacheTTL = %s, want 4h", cfg.OAuthTokenCacheTTL)
	}
}

// TestLoad_TOML_File asserts Load() reads a TOML file when one is
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

	cfg, _ := Load()
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

// TestLoad_PrecedenceTOMLOverridesDefault — when TOML sets a field and
// no env override exists, the TOML value beats the code default.
func TestLoad_PrecedenceTOMLOverridesDefault(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	chdirTo(t, dir)
	writeTOML(t, dir, `port = 7000
docs_dir = "/etc/satellites/docs"
`)

	cfg, _ := Load()
	if cfg.Port != 7000 {
		t.Errorf("Port = %d, want 7000 (TOML overrides default 8080)", cfg.Port)
	}
	if cfg.DocsDir != "/etc/satellites/docs" {
		t.Errorf("DocsDir = %q, want /etc/satellites/docs (TOML overrides /app/docs)", cfg.DocsDir)
	}
}

// TestLoad_PrecedenceEnvOverridesTOML — when both env var and TOML set
// the same field, env wins. Covers one int (PORT), one string (LOG_LEVEL),
// one bool (SATELLITES_GRANTS_ENFORCED), and one duration
// (SATELLITES_OAUTH_TOKEN_CACHE_TTL).
func TestLoad_PrecedenceEnvOverridesTOML(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	chdirTo(t, dir)
	writeTOML(t, dir, `port = 7000
log_level = "debug"
grants_enforced = false
oauth_token_cache_ttl = "10m"
`)
	t.Setenv("SATELLITES_PORT", "9999")
	t.Setenv("SATELLITES_LOG_LEVEL", "warn")
	t.Setenv("SATELLITES_GRANTS_ENFORCED", "true")
	t.Setenv("SATELLITES_OAUTH_TOKEN_CACHE_TTL", "30s")

	cfg, _ := Load()
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
// SATELLITES_CONFIG path that doesn't exist warns and falls through to
// defaults+env (the service still boots).
func TestLoad_SATELLITES_CONFIG_MissingFile(t *testing.T) {
	clearEnv(t)
	chdirTo(t, t.TempDir())
	t.Setenv("SATELLITES_CONFIG", "/nonexistent/satellites.toml")

	cfg, warnings := Load()
	if cfg == nil {
		t.Fatalf("Load() cfg = nil, want non-nil")
	}
	if !containsContaining(warnings, "/nonexistent/satellites.toml") {
		t.Errorf("warnings missing missing-file notice; got %v", warnings)
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

	cfg, _ := Load()
	if cfg.Port != 2222 {
		t.Errorf("Port = %d, want 2222 (SATELLITES_CONFIG path), got cwd value instead", cfg.Port)
	}
}

// TestLoad_TOMLParseError_Warns asserts that a malformed TOML file emits
// a warning and the service still boots on defaults+env.
func TestLoad_TOMLParseError_Warns(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	chdirTo(t, dir)
	writeTOML(t, dir, "this is = = not valid = toml [[[")

	cfg, warnings := Load()
	if cfg == nil {
		t.Fatalf("Load() cfg = nil, want non-nil")
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080 default after TOML parse failure", cfg.Port)
	}
	if !containsContaining(warnings, "parse failed") {
		t.Errorf("warnings missing parse-failed notice; got %v", warnings)
	}
}
