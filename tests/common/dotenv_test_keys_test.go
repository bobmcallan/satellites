package common

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func TestDotenv_FileMissing_NoError(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("SATELLITES_GEMINI_API_KEY", "")
	loadDotenvFile(filepath.Join(tmp, ".env"))
	assert.Empty(t, os.Getenv("SATELLITES_GEMINI_API_KEY"))
}

func TestDotenv_MalformedLinesSkipped(t *testing.T) {
	tmp := t.TempDir()
	envPath := filepath.Join(tmp, ".env")
	writeFile(t, envPath, "# comment line\n\nno-equals-sign\n=missing-key\nSATELLITES_GEMINI_API_KEY=ok\n")

	t.Setenv("SATELLITES_GEMINI_API_KEY", "")
	loadDotenvFile(envPath)

	assert.Equal(t, "ok", os.Getenv("SATELLITES_GEMINI_API_KEY"))
}

func TestDotenv_NonWhitelistedKeyIgnored(t *testing.T) {
	tmp := t.TempDir()
	envPath := filepath.Join(tmp, ".env")
	writeFile(t, envPath, "EVIL_KEY=secret\nSATELLITES_GEMINI_API_KEY=ok\n")

	t.Setenv("EVIL_KEY", "")
	t.Setenv("SATELLITES_GEMINI_API_KEY", "")
	loadDotenvFile(envPath)

	assert.Empty(t, os.Getenv("EVIL_KEY"), "non-whitelisted keys must not be propagated")
	assert.Equal(t, "ok", os.Getenv("SATELLITES_GEMINI_API_KEY"))
}

func TestDotenv_HostExportedWins(t *testing.T) {
	tmp := t.TempDir()
	envPath := filepath.Join(tmp, ".env")
	writeFile(t, envPath, "SATELLITES_GEMINI_API_KEY=from-file\n")

	t.Setenv("SATELLITES_GEMINI_API_KEY", "from-host")
	loadDotenvFile(envPath)

	assert.Equal(t, "from-host", os.Getenv("SATELLITES_GEMINI_API_KEY"), "host-exported value must win")
}

func TestDotenv_WalkBoundedAtTests(t *testing.T) {
	tmp := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, "tests", "common"), 0o755))
	writeFile(t, filepath.Join(tmp, "tests", ".env"), "SATELLITES_GEMINI_API_KEY=tests-env\n")
	writeFile(t, filepath.Join(tmp, ".env"), "SATELLITES_GEMINI_API_KEY=root-env\n")

	resolved := findTestsEnvFile(filepath.Join(tmp, "tests", "common"))
	assert.Equal(t, filepath.Join(tmp, "tests", ".env"), resolved,
		"walk must stop at the tests/ ancestor, not escape to repo root")
}

func TestDotenv_WalkReturnsEmptyWhenNoTestsAncestor(t *testing.T) {
	tmp := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, "src", "pkg"), 0o755))
	resolved := findTestsEnvFile(filepath.Join(tmp, "src", "pkg"))
	assert.Equal(t, "", resolved, "no tests/ ancestor → no env path")
}

func TestDotenv_QuotedValuesStripped(t *testing.T) {
	tmp := t.TempDir()
	envPath := filepath.Join(tmp, ".env")
	writeFile(t, envPath, `SATELLITES_GEMINI_API_KEY="quoted-value"`+"\n"+`SATELLITES_GEMINI_REVIEW_MODEL='single-quoted'`+"\n")

	t.Setenv("SATELLITES_GEMINI_API_KEY", "")
	t.Setenv("SATELLITES_GEMINI_REVIEW_MODEL", "")
	loadDotenvFile(envPath)

	assert.Equal(t, "quoted-value", os.Getenv("SATELLITES_GEMINI_API_KEY"))
	assert.Equal(t, "single-quoted", os.Getenv("SATELLITES_GEMINI_REVIEW_MODEL"))
}

func TestDotenv_ExportPrefixTolerated(t *testing.T) {
	tmp := t.TempDir()
	envPath := filepath.Join(tmp, ".env")
	writeFile(t, envPath, "export SATELLITES_GEMINI_API_KEY=exported\n")

	t.Setenv("SATELLITES_GEMINI_API_KEY", "")
	loadDotenvFile(envPath)

	assert.Equal(t, "exported", os.Getenv("SATELLITES_GEMINI_API_KEY"))
}

func TestDotenv_AllWhitelistedKeysAccepted(t *testing.T) {
	tmp := t.TempDir()
	envPath := filepath.Join(tmp, ".env")
	writeFile(t, envPath,
		"SATELLITES_GEMINI_API_KEY=k1\n"+
			"SATELLITES_GEMINI_REVIEW_MODEL=m1\n"+
			"SATELLITES_EMBEDDINGS_API_KEY=k2\n"+
			"SATELLITES_EMBEDDINGS_PROVIDER=p1\n"+
			"SATELLITES_EMBEDDINGS_MODEL=m2\n",
	)

	for _, k := range []string{"SATELLITES_GEMINI_API_KEY", "SATELLITES_GEMINI_REVIEW_MODEL", "SATELLITES_EMBEDDINGS_API_KEY", "SATELLITES_EMBEDDINGS_PROVIDER", "SATELLITES_EMBEDDINGS_MODEL"} {
		t.Setenv(k, "")
	}
	loadDotenvFile(envPath)

	assert.Equal(t, "k1", os.Getenv("SATELLITES_GEMINI_API_KEY"))
	assert.Equal(t, "m1", os.Getenv("SATELLITES_GEMINI_REVIEW_MODEL"))
	assert.Equal(t, "k2", os.Getenv("SATELLITES_EMBEDDINGS_API_KEY"))
	assert.Equal(t, "p1", os.Getenv("SATELLITES_EMBEDDINGS_PROVIDER"))
	assert.Equal(t, "m2", os.Getenv("SATELLITES_EMBEDDINGS_MODEL"))
}
