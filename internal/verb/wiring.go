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

// toolsListChangedNotifier, when set by the MCP server, is invoked after a
// grant change that could alter a connected client's role-filtered tool list
// (workspace membership add/update/remove). The MCP server binds it to a
// notifications/tools/list_changed broadcast so a connected client re-lists;
// a stale cached list still fails closed at dispatch. nil on the CLI-local
// path (no MCP transport) — notifyToolsListChanged is then a no-op.
var toolsListChangedNotifier func()

// SetToolsListChangedNotifier wires the MCP server's list-changed broadcast
// into the verb package. Called once at server boot.
func SetToolsListChangedNotifier(fn func()) { toolsListChangedNotifier = fn }

// notifyToolsListChanged fires the registered list-changed notifier, if any.
func notifyToolsListChanged() {
	if toolsListChangedNotifier != nil {
		toolsListChangedNotifier()
	}
}
