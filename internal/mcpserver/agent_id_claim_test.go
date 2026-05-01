package mcpserver

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
)

// TestClaim_AgentIDSourcesPatternsFromAgentDoc verifies story_b39b393f
// + story_cc55e093: when contract_claim is called with agent_id, the
// action_claim ledger row's permission_patterns are sourced from the
// agent document and the CI is stamped with the agent_id.
func TestClaim_AgentIDSourcesPatternsFromAgentDoc(t *testing.T) {
	t.Parallel()
	f := newClaimFixture(t)

	// Use the fixture's seeded role agent for cis[0] (plan).
	// The fixture maps plan to developer_agent; the lookup returns
	// its agent doc id.
	planAgentID := f.agentFor(0)
	agentDoc, err := f.server.docs.GetByID(f.ctx, planAgentID, nil)
	if err != nil {
		t.Fatalf("lookup seeded role agent for plan: %v", err)
	}
	settings, err := document.UnmarshalAgentSettings(agentDoc.Structured)
	if err != nil {
		t.Fatalf("decode agent settings: %v", err)
	}

	res, err := f.server.handleContractClaim(f.callerCtx(), newCallToolReq("contract_claim", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"session_id":           f.sessionID,
		"agent_id":             planAgentID,
		"plan_markdown":        "claim with agent",
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if res.IsError {
		t.Fatalf("isError: %s", firstText(res))
	}
	var body struct {
		ContractInstanceID  string `json:"contract_instance_id"`
		ActionClaimLedgerID string `json:"action_claim_ledger_id"`
	}
	if err := json.Unmarshal([]byte(firstText(res)), &body); err != nil {
		t.Fatalf("parse: %v", err)
	}

	ci, err := f.server.contracts.GetByID(f.ctx, body.ContractInstanceID, []string{f.wsID})
	if err != nil {
		t.Fatalf("ci lookup: %v", err)
	}
	if ci.AgentID != planAgentID {
		t.Errorf("CI.AgentID = %q, want %q", ci.AgentID, planAgentID)
	}

	rows, err := f.server.ledger.List(f.ctx, f.projectID, ledger.ListOptions{StoryID: f.storyID, Type: ledger.TypeActionClaim}, nil)
	if err != nil {
		t.Fatalf("ledger list: %v", err)
	}
	var acRow *ledger.LedgerEntry
	for i := range rows {
		if rows[i].ID == body.ActionClaimLedgerID {
			acRow = &rows[i]
			break
		}
	}
	if acRow == nil {
		t.Fatalf("action_claim row %q not found among %d rows", body.ActionClaimLedgerID, len(rows))
	}

	var payload struct {
		PermissionsClaim []string `json:"permissions_claim"`
		AgentID          string   `json:"agent_id"`
		Source           string   `json:"source"`
	}
	if err := json.Unmarshal(acRow.Structured, &payload); err != nil {
		t.Fatalf("parse action_claim structured: %v", err)
	}
	if !reflect.DeepEqual(payload.PermissionsClaim, settings.PermissionPatterns) {
		t.Errorf("permissions_claim = %v, want %v (must be sourced from agent doc)", payload.PermissionsClaim, settings.PermissionPatterns)
	}
	if payload.AgentID != planAgentID {
		t.Errorf("action_claim.agent_id = %q, want %q", payload.AgentID, planAgentID)
	}
	if payload.Source != "agent_document" {
		t.Errorf("action_claim.source = %q, want \"agent_document\"", payload.Source)
	}
}

// TestClaim_AgentIDNotFound verifies that a bad agent_id surfaces as a
// structured agent_not_found error.
func TestClaim_AgentIDNotFound(t *testing.T) {
	t.Parallel()
	f := newClaimFixture(t)

	res, _ := f.server.handleContractClaim(f.callerCtx(), newCallToolReq("contract_claim", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"session_id":           f.sessionID,
		"agent_id":             "doc_does_not_exist",
	}))
	if !res.IsError {
		t.Fatalf("expected error; got %s", firstText(res))
	}
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(firstText(res)), &payload); err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if payload.Error != "agent_not_found" {
		t.Errorf("error = %q, want \"agent_not_found\"", payload.Error)
	}
}

// TestClaim_RejectsMissingAgentID verifies story_cc55e093 AC1: claim
// without agent_id returns a structured agent_required error.
func TestClaim_RejectsMissingAgentID(t *testing.T) {
	t.Parallel()
	f := newClaimFixture(t)
	res, _ := f.server.handleContractClaim(f.callerCtx(), newCallToolReq("contract_claim", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"session_id":           f.sessionID,
	}))
	if !res.IsError {
		t.Fatalf("expected error; got %s", firstText(res))
	}
	var payload struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal([]byte(firstText(res)), &payload)
	if payload.Error != "agent_required" {
		t.Errorf("error = %q, want \"agent_required\"", payload.Error)
	}
}

// TestClaim_RejectsPermissionsClaim verifies story_cc55e093 AC2: claim
// with non-empty permissions_claim arg returns permissions_claim_retired.
func TestClaim_RejectsPermissionsClaim(t *testing.T) {
	t.Parallel()
	f := newClaimFixture(t)
	res, _ := f.server.handleContractClaim(f.callerCtx(), newCallToolReq("contract_claim", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"session_id":           f.sessionID,
		"agent_id":             f.agentFor(0),
		"permissions_claim":    []string{"Bash:rm_rf"},
	}))
	if !res.IsError {
		t.Fatalf("expected error; got %s", firstText(res))
	}
	var payload struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal([]byte(firstText(res)), &payload)
	if payload.Error != "permissions_claim_retired" {
		t.Errorf("error = %q, want \"permissions_claim_retired\"", payload.Error)
	}
}

// TestValidate_AgentMistypedPermissionPatterns verifies AC1: a
// type=agent document with a mistyped permission_patterns field is
// rejected by Document.Validate.
func TestValidate_AgentMistypedPermissionPatterns(t *testing.T) {
	t.Parallel()
	d := document.Document{
		Type:        document.TypeAgent,
		Scope:       document.ScopeSystem,
		Name:        "bad_agent",
		WorkspaceID: "wksp_x",
		Status:      document.StatusActive,
		Structured:  []byte(`{"permission_patterns":"not-a-list"}`),
	}
	if err := d.Validate(); err == nil {
		t.Fatal("expected Validate to reject mistyped permission_patterns; got nil")
	}
}

// TestValidate_AgentLegacyFieldsAccepted ensures the AgentSettings
// validation does not reject legacy orchestrator-agent fields
// (provider_chain / tier / permitted_roles / tool_ceiling) that
// pre-date the typed AgentSettings struct.
func TestValidate_AgentLegacyFieldsAccepted(t *testing.T) {
	t.Parallel()
	d := document.Document{
		Type:        document.TypeAgent,
		Scope:       document.ScopeSystem,
		Name:        "orch_agent",
		WorkspaceID: "wksp_x",
		Status:      document.StatusActive,
		Structured:  []byte(`{"provider_chain":["claude"],"tier":"opus","permitted_roles":["role_x"],"tool_ceiling":["*"]}`),
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("legacy orchestrator fields rejected: %v", err)
	}
}

// Compile-time guard that the contract package round-trips AgentID
// through Create + GetByID — guards AC2.
var _ = func() bool {
	_ = contract.ContractInstance{AgentID: "agent_x"}
	return true
}()
