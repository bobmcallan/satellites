package verb

import (
	"context"
	"testing"

	"github.com/bobmcallan/satellites/internal/auth"
)

// TestUpsertStatusHonoured_TransportBased (sty_4f5148c4): document_upsert status
// is refused over the MCP transport (regardless of credential — closing the
// OAuth-MCP hole), refused for api-key callers (sty_42d13ae4, preserved), and
// honoured only for the portal/in-process caller.
func TestUpsertStatusHonoured_TransportBased(t *testing.T) {
	// MCP transport → never, even with no api-key role (the OAuth-MCP session).
	if upsertStatusHonoured(auth.WithTransport(context.Background(), auth.TransportMCP)) {
		t.Error("MCP transport must NOT honour a status write")
	}
	// MCP + an api-key role → also never.
	mcpKeyed := auth.WithAPIKeyRole(auth.WithTransport(context.Background(), auth.TransportMCP), auth.APIKeyRoleExecutor)
	if upsertStatusHonoured(mcpKeyed) {
		t.Error("MCP + api-key must NOT honour status")
	}
	// Non-MCP, no api-key (portal JWT / in-process CLI) → honoured.
	if !upsertStatusHonoured(context.Background()) {
		t.Error("in-process/portal caller should honour status")
	}
	// Non-MCP api-key (exec / gate key) → not honoured (sty_42d13ae4 preserved).
	if upsertStatusHonoured(auth.WithAPIKeyRole(context.Background(), auth.APIKeyRoleExecutor)) {
		t.Error("api-key caller should not honour status")
	}
}
