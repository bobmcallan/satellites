package satellitesinit

import (
	_ "embed"
	"sync"
)

//go:embed embedded/satellites_client_install.md
var installSchemaMD []byte

// EmbeddedMarkdown returns the raw bytes of the embedded
// install-schema markdown source. Exposed so tools that want to
// re-seed a DB-backed copy can read the same source the server uses.
func EmbeddedMarkdown() []byte { return installSchemaMD }

var (
	parseOnce sync.Once
	parsed    InstallSchema
	parseErr  error
)

// Embedded returns the InstallSchema parsed from the embedded
// markdown. Parsed lazily and cached for the process lifetime — the
// markdown bytes are immutable for a given binary, so re-parsing on
// every verb invocation would be pure overhead.
func Embedded() (InstallSchema, error) {
	parseOnce.Do(func() {
		parsed, parseErr = ParseFrontmatter(installSchemaMD)
	})
	return parsed, parseErr
}
