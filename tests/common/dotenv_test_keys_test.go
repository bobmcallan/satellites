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
	t.Setenv("GEMINI_API_KEY", "")
	loadDotenvFile(filepath.Join(tmp, ".env"))
	assert.Empty(t, os.Getenv("GEMINI_API_KEY"))
}

func TestDotenv_MalformedLinesSkipped(t *testing.T) {
	tmp := t.TempDir()
	envPath := filepath.Join(tmp, ".env")
	writeFile(t, envPath, "# comment line\n\nno-equals-sign\n=missing-key\nGEMINI_API_KEY=ok\n")

	t.Setenv("GEMINI_API_KEY", "")
	loadDotenvFile(envPath)

	assert.Equal(t, "ok", os.Getenv("GEMINI_API_KEY"))
}

func TestDotenv_NonWhitelistedKeyIgnored(t *testing.T) {
	tmp := t.TempDir()
	envPath := filepath.Join(tmp, ".env")
	writeFile(t, envPath, "EVIL_KEY=secret\nGEMINI_API_KEY=ok\n")

	t.Setenv("EVIL_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	loadDotenvFile(envPath)

	assert.Empty(t, os.Getenv("EVIL_KEY"), "non-whitelisted keys must not be propagated")
	assert.Equal(t, "ok", os.Getenv("GEMINI_API_KEY"))
}

func TestDotenv_HostExportedWins(t *testing.T) {
	tmp := t.TempDir()
	envPath := filepath.Join(tmp, ".env")
	writeFile(t, envPath, "GEMINI_API_KEY=from-file\n")

	t.Setenv("GEMINI_API_KEY", "from-host")
	loadDotenvFile(envPath)

	assert.Equal(t, "from-host", os.Getenv("GEMINI_API_KEY"), "host-exported value must win")
}

func TestDotenv_WalkBoundedAtTests(t *testing.T) {
	tmp := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, "tests", "common"), 0o755))
	writeFile(t, filepath.Join(tmp, "tests", ".env"), "GEMINI_API_KEY=tests-env\n")
	writeFile(t, filepath.Join(tmp, ".env"), "GEMINI_API_KEY=root-env\n")

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
	writeFile(t, envPath, `GEMINI_API_KEY="quoted-value"`+"\n"+`GEMINI_REVIEW_MODEL='single-quoted'`+"\n")

	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GEMINI_REVIEW_MODEL", "")
	loadDotenvFile(envPath)

	assert.Equal(t, "quoted-value", os.Getenv("GEMINI_API_KEY"))
	assert.Equal(t, "single-quoted", os.Getenv("GEMINI_REVIEW_MODEL"))
}

func TestDotenv_ExportPrefixTolerated(t *testing.T) {
	tmp := t.TempDir()
	envPath := filepath.Join(tmp, ".env")
	writeFile(t, envPath, "export GEMINI_API_KEY=exported\n")

	t.Setenv("GEMINI_API_KEY", "")
	loadDotenvFile(envPath)

	assert.Equal(t, "exported", os.Getenv("GEMINI_API_KEY"))
}

func TestDotenv_AllWhitelistedKeysAccepted(t *testing.T) {
	tmp := t.TempDir()
	envPath := filepath.Join(tmp, ".env")
	writeFile(t, envPath,
		"GEMINI_API_KEY=k1\n"+
			"GEMINI_REVIEW_MODEL=m1\n"+
			"EMBEDDINGS_API_KEY=k2\n"+
			"EMBEDDINGS_PROVIDER=p1\n"+
			"EMBEDDINGS_MODEL=m2\n",
	)

	for _, k := range []string{"GEMINI_API_KEY", "GEMINI_REVIEW_MODEL", "EMBEDDINGS_API_KEY", "EMBEDDINGS_PROVIDER", "EMBEDDINGS_MODEL"} {
		t.Setenv(k, "")
	}
	loadDotenvFile(envPath)

	assert.Equal(t, "k1", os.Getenv("GEMINI_API_KEY"))
	assert.Equal(t, "m1", os.Getenv("GEMINI_REVIEW_MODEL"))
	assert.Equal(t, "k2", os.Getenv("EMBEDDINGS_API_KEY"))
	assert.Equal(t, "p1", os.Getenv("EMBEDDINGS_PROVIDER"))
	assert.Equal(t, "m2", os.Getenv("EMBEDDINGS_MODEL"))
}
