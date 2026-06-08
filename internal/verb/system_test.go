package verb

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
)

// dispatchStatus is a small helper: dispatch system_status with the given
// request and decode the response, failing the test on any error.
func dispatchStatus(t *testing.T, req SystemStatusRequest) SystemStatusResponse {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal req: %v", err)
	}
	resp, err := Dispatch(context.Background(), "system_status", raw)
	if err != nil {
		t.Fatalf("dispatch system_status: %v", err)
	}
	var out SystemStatusResponse
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out
}

// TestSystemStatus_HappyShape covers AC#2/#5/#7: the response carries the
// server build block, a parseable RFC3339 started_at, and a non-negative
// uptime derived from process boot. Also asserts the verb is registered and
// dispatchable via verb.Dispatch (the layering contract).
func TestSystemStatus_HappyShape(t *testing.T) {
	if Get("system_status") == nil {
		t.Fatal("system_status not registered in the verb registry")
	}
	out := dispatchStatus(t, SystemStatusRequest{ClientVersion: "v9.9.9"})

	if out.Server.Version == "" || out.Server.Commit == "" || out.Server.BuiltAt == "" {
		t.Errorf("server block incomplete: %+v", out.Server)
	}
	if _, err := time.Parse(time.RFC3339, out.StartedAt); err != nil {
		t.Errorf("started_at %q not RFC3339: %v", out.StartedAt, err)
	}
	if out.UptimeSeconds < 0 {
		t.Errorf("uptime_seconds negative: %d", out.UptimeSeconds)
	}
	if out.Client == nil || out.Client.Version != "v9.9.9" {
		t.Errorf("client_version not echoed: %+v", out.Client)
	}
}

// TestSystemStatus_ClientVersionOptional covers AC#3: an absent
// client_version is omitted from the response and is not an error.
func TestSystemStatus_ClientVersionOptional(t *testing.T) {
	out := dispatchStatus(t, SystemStatusRequest{}) // no client_version
	if out.Client != nil {
		t.Errorf("client should be omitted when client_version absent, got %+v", out.Client)
	}
	// Empty payload (nil body) must also be accepted.
	resp, err := Dispatch(context.Background(), "system_status", nil)
	if err != nil {
		t.Fatalf("dispatch with nil body: %v", err)
	}
	var out2 SystemStatusResponse
	if err := json.Unmarshal(resp, &out2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out2.Client != nil {
		t.Errorf("client should be omitted for nil body, got %+v", out2.Client)
	}
}

// TestSystemStatus_DBDown covers AC#4: when the substrate DB is unreachable,
// the ping fails into db.ok=false + db.error and the verb itself still
// returns success (a health probe reports failure, it does not become one).
func TestSystemStatus_DBDown(t *testing.T) {
	// Point the store at a DB that cannot connect (refused port). sql.Open
	// is lazy; PingContext is the round trip that fails.
	db, err := sql.Open("postgres", "postgres://127.0.0.1:1/none?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	prev := documentStore
	documentStore = &document.Store{DB: db}
	t.Cleanup(func() { documentStore = prev })

	out := dispatchStatus(t, SystemStatusRequest{})
	if out.DB.OK {
		t.Errorf("db.ok should be false against an unreachable DB")
	}
	if out.DB.Error == "" {
		t.Errorf("db.error should be populated on ping failure")
	}
}

// TestSystemStatus_StoreUnconfigured covers the nil-store branch: db.ok=false
// with an explanatory error, verb still succeeds.
func TestSystemStatus_StoreUnconfigured(t *testing.T) {
	prev := documentStore
	documentStore = nil
	t.Cleanup(func() { documentStore = prev })

	out := dispatchStatus(t, SystemStatusRequest{})
	if out.DB.OK || out.DB.Error == "" {
		t.Errorf("nil store should yield db.ok=false + error, got %+v", out.DB)
	}
}
