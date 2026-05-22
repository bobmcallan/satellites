package verb

import "github.com/bobmcallan/satellites/internal/auth"

// authStore is the server-side authentication store, set by
// cmd/satellites-server at boot. Verbs that distinguish authenticated
// from CLI-local in-process callers read it via authStore != nil; the
// in-process CLI path leaves it unset and skips the membership checks
// the HTTP/MCP path enforces.
var authStore *auth.Store

// SetAuthStore wires the server's auth.Store into the verb package.
// Called once at server boot; pass nil from tests that need to reset
// the package-level handle between cases.
func SetAuthStore(s *auth.Store) { authStore = s }
