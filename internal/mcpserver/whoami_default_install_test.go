package mcpserver

import (
	"encoding/json"
	"testing"

	"github.com/bobmcallan/satellites/internal/ledger"
)

// TestSessionWhoami_InstallsSessionDefaultLedgerRow verifies story_488b8223
// AC2: handleSessionWhoami writes a kind:session-default-install
// ledger row when called against a session with an OrchestratorGrantID
// set, and the row is idempotent within the staleness window.
func TestSessionWhoami_InstallsSessionDefaultLedgerRow(t *testing.T) {
	t.Parallel()
	f := newClaimFixture(t)

	// First whoami: row should be installed.
	res, err := f.server.handleSessionWhoami(f.callerCtx(), newCallToolReq("session_whoami", map[string]any{
		"session_id": f.sessionID,
	}))
	if err != nil {
		t.Fatalf("whoami: %v", err)
	}
	if res.IsError {
		t.Fatalf("isError: %s", firstText(res))
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(firstText(res)), &first); err != nil {
		t.Fatalf("parse: %v", err)
	}
	rowID, _ := first["session_default_install_ledger_id"].(string)
	if rowID == "" {
		t.Fatal("expected session_default_install_ledger_id on first whoami; got none")
	}

	rows, err := f.server.ledger.List(f.ctx, "", ledger.ListOptions{
		Type: ledger.TypeDecision,
		Tags: []string{"kind:session-default-install", "session:" + f.sessionID},
	}, nil)
	if err != nil {
		t.Fatalf("ledger list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 install row after first whoami, got %d", len(rows))
	}

	// Second whoami within the same staleness window: no duplicate row.
	res2, err := f.server.handleSessionWhoami(f.callerCtx(), newCallToolReq("session_whoami", map[string]any{
		"session_id": f.sessionID,
	}))
	if err != nil {
		t.Fatalf("second whoami: %v", err)
	}
	if res2.IsError {
		t.Fatalf("second isError: %s", firstText(res2))
	}
	var second map[string]any
	_ = json.Unmarshal([]byte(firstText(res2)), &second)
	if id, ok := second["session_default_install_ledger_id"].(string); ok && id != "" {
		t.Errorf("second whoami wrote a new install row %q (expected idempotent skip)", id)
	}

	rows, _ = f.server.ledger.List(f.ctx, "", ledger.ListOptions{
		Type: ledger.TypeDecision,
		Tags: []string{"kind:session-default-install", "session:" + f.sessionID},
	}, nil)
	if len(rows) != 1 {
		t.Errorf("install row count after second whoami = %d, want 1 (idempotence violated)", len(rows))
	}
}
