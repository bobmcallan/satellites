package permhook

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bobmcallan/satellites/internal/ledger"
)

// TestHandler_DenyOrchestratorWrite verifies AC4: an orchestrator
// session-default that does NOT include Write blocks Write tools.
func TestHandler_DenyOrchestratorWrite(t *testing.T) {
	t.Parallel()
	f := newResolveFixture(t)
	f.appendSessionDefault(t, "agent_orch", []string{"Read:**", "mcp__satellites__satellites_*"})

	h := &Handler{Resolver: f.r}
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	body, _ := json.Marshal(hookRequest{Tool: "Write:internal/foo.go", SessionID: f.sessID})
	resp, err := http.Post(srv.URL+"/hooks/enforce", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got Result
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Decision != DecisionDeny || got.Source != SourceSessionDefault {
		t.Errorf("orchestrator Write = %+v, want deny session_default", got)
	}
}

// TestHandler_AllowDevelopEdit verifies AC5: develop CI claimed →
// developer_agent.permission_patterns admit Edit.
func TestHandler_AllowDevelopEdit(t *testing.T) {
	t.Parallel()
	f := newResolveFixture(t)
	f.appendActionClaim(t, "agent_dev", []string{"Edit:**", "Bash:go_test"}, ledger.StatusActive)

	h := &Handler{Resolver: f.r}
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	body, _ := json.Marshal(hookRequest{Tool: "Edit:internal/foo.go", SessionID: f.sessID})
	resp, err := http.Post(srv.URL+"/hooks/enforce", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	var got Result
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got.Decision != DecisionAllow || got.Source != SourceActiveCI {
		t.Errorf("develop edit = %+v, want allow active_ci", got)
	}
}

// TestHandler_BadRequestRejected covers the malformed-payload path.
func TestHandler_BadRequestRejected(t *testing.T) {
	t.Parallel()
	f := newResolveFixture(t)
	h := &Handler{Resolver: f.r}
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/hooks/enforce", "application/json", bytes.NewReader([]byte("not-json")))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}
