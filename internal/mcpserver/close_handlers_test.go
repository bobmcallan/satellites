package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/session"
)

// closeFixture is newClaimFixture with a helper for claiming a CI.
type closeFixture struct {
	*claimFixture
}

func newCloseFixture(t *testing.T) *closeFixture {
	return &closeFixture{claimFixture: newClaimFixture(t)}
}

// claim advances a CI from ready → claimed via the handler so the
// subsequent close path has a realistic input.
func (f *closeFixture) claim(t *testing.T, idx int, plan string) {
	t.Helper()
	res, err := f.server.handleStoryContractClaim(f.callerCtx(), newCallToolReq("story_contract_claim", map[string]any{
		"contract_instance_id": f.cis[idx].ID,
		"session_id":           f.sessionID,
		"plan_markdown":        plan,
	}))
	if err != nil || res.IsError {
		t.Fatalf("claim[%d]: err=%v text=%s", idx, err, firstText(res))
	}
}

func TestClose_HappyPath(t *testing.T) {
	t.Parallel()
	f := newCloseFixture(t)
	f.claim(t, 0, "plan body")

	res, err := f.server.handleStoryContractClose(f.callerCtx(), newCallToolReq("story_contract_close", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"close_markdown":       "done",
		"evidence_markdown":    "evidence body",
	}))
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if res.IsError {
		t.Fatalf("close isError: %s", firstText(res))
	}
	var body struct {
		Status           string `json:"status"`
		CloseLedgerID    string `json:"close_ledger_id"`
		EvidenceLedgerID string `json:"evidence_ledger_id"`
		StoryStatus      string `json:"story_status"`
	}
	if err := json.Unmarshal([]byte(firstText(res)), &body); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if body.Status != contract.StatusPassed {
		t.Fatalf("status: %q", body.Status)
	}
	if body.CloseLedgerID == "" {
		t.Fatalf("missing close_ledger_id")
	}
	if body.EvidenceLedgerID == "" {
		t.Fatalf("missing evidence_ledger_id")
	}
	if body.StoryStatus != "" {
		t.Fatalf("story should still be in_progress: %q", body.StoryStatus)
	}
}

func TestClose_EvidenceAndCloseRequestRows(t *testing.T) {
	t.Parallel()
	f := newCloseFixture(t)
	f.claim(t, 0, "")
	res, _ := f.server.handleStoryContractClose(f.callerCtx(), newCallToolReq("story_contract_close", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"close_markdown":       "done",
		"evidence_markdown":    "ev",
	}))
	var body struct {
		CloseLedgerID    string `json:"close_ledger_id"`
		EvidenceLedgerID string `json:"evidence_ledger_id"`
	}
	_ = json.Unmarshal([]byte(firstText(res)), &body)

	closeRow, err := f.server.ledger.GetByID(f.ctx, body.CloseLedgerID, nil)
	if err != nil {
		t.Fatalf("load close: %v", err)
	}
	if closeRow.Type != ledger.TypeCloseRequest {
		t.Fatalf("close type: %q", closeRow.Type)
	}
	hasCloseTag, hasPhase := false, false
	for _, tag := range closeRow.Tags {
		if tag == "kind:close-request" {
			hasCloseTag = true
		}
		if tag == "phase:close" {
			hasPhase = true
		}
	}
	if !hasCloseTag || !hasPhase {
		t.Fatalf("close row tags: %v", closeRow.Tags)
	}

	evRow, err := f.server.ledger.GetByID(f.ctx, body.EvidenceLedgerID, nil)
	if err != nil {
		t.Fatalf("load evidence: %v", err)
	}
	if evRow.Type != ledger.TypeEvidence {
		t.Fatalf("evidence type: %q", evRow.Type)
	}
}

func TestClose_RollsStoryToDone(t *testing.T) {
	t.Parallel()
	f := newCloseFixture(t)
	// Close CI[0..2] (required_for_close=true); CI[3] is the optional
	// story_close slot.
	for i := 0; i < 3; i++ {
		f.claim(t, i, "")
		res, _ := f.server.handleStoryContractClose(f.callerCtx(), newCallToolReq("story_contract_close", map[string]any{
			"contract_instance_id": f.cis[i].ID,
			"close_markdown":       fmt.Sprintf("close %d", i),
		}))
		if res.IsError {
			t.Fatalf("close[%d]: %s", i, firstText(res))
		}
	}
	// Verify story transitioned to done.
	st, err := f.server.stories.GetByID(f.ctx, f.storyID, nil)
	if err != nil {
		t.Fatalf("load story: %v", err)
	}
	if st.Status != "done" {
		t.Fatalf("story status: %q want done", st.Status)
	}
}

func TestClose_PreplanReentryWorkflowClaim(t *testing.T) {
	t.Parallel()
	f := newCloseFixture(t)
	f.claim(t, 0, "")
	res, err := f.server.handleStoryContractClose(f.callerCtx(), newCallToolReq("story_contract_close", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"close_markdown":       "preplan done",
		"proposed_workflow":    []string{"preplan", "plan", "develop", "story_close"},
	}))
	if err != nil || res.IsError {
		t.Fatalf("close: err=%v text=%s", err, firstText(res))
	}
	var body struct {
		WorkflowClaimLedgerID string `json:"workflow_claim_ledger_id"`
	}
	_ = json.Unmarshal([]byte(firstText(res)), &body)
	if body.WorkflowClaimLedgerID == "" {
		t.Fatalf("expected workflow_claim_ledger_id")
	}
	row, err := f.server.ledger.GetByID(f.ctx, body.WorkflowClaimLedgerID, nil)
	if err != nil {
		t.Fatalf("load workflow-claim: %v", err)
	}
	if row.Type != ledger.TypeWorkflowClaim {
		t.Fatalf("type: %q", row.Type)
	}
}

func TestClose_PreplanWorkflowSpecInvalid(t *testing.T) {
	t.Parallel()
	f := newCloseFixture(t)
	f.claim(t, 0, "")
	res, err := f.server.handleStoryContractClose(f.callerCtx(), newCallToolReq("story_contract_close", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"close_markdown":       "invalid shape",
		"proposed_workflow":    []string{"plan", "develop", "story_close"}, // missing preplan
	}))
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	text := firstText(res)
	if !anySubstring(text, `"error":"missing_required_slot"`, `"contract_name":"preplan"`) {
		t.Fatalf("expected spec error, got %s", text)
	}
}

func TestClose_PlanDeferred(t *testing.T) {
	t.Parallel()
	f := newCloseFixture(t)
	// Claim without a plan (deferred).
	if _, err := f.server.handleStoryContractClaim(f.callerCtx(), newCallToolReq("story_contract_claim", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"session_id":           f.sessionID,
	})); err != nil {
		t.Fatalf("claim: %v", err)
	}

	res, err := f.server.handleStoryContractClose(f.callerCtx(), newCallToolReq("story_contract_close", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"close_markdown":       "plan deferred",
		"plan_markdown":        "the deferred plan",
	}))
	if err != nil || res.IsError {
		t.Fatalf("close: err=%v text=%s", err, firstText(res))
	}
	var body struct {
		PlanLedgerID string `json:"plan_ledger_id"`
	}
	_ = json.Unmarshal([]byte(firstText(res)), &body)
	if body.PlanLedgerID == "" {
		t.Fatalf("plan_ledger_id should be populated for deferred plan")
	}
	// CI should carry the new PlanLedgerID.
	ci, _ := f.server.contracts.GetByID(f.ctx, f.cis[0].ID, nil)
	if ci.PlanLedgerID != body.PlanLedgerID {
		t.Fatalf("CI PlanLedgerID: got %q want %q", ci.PlanLedgerID, body.PlanLedgerID)
	}
}

func TestRespond_WritesRow(t *testing.T) {
	t.Parallel()
	f := newCloseFixture(t)
	f.claim(t, 0, "")
	res, err := f.server.handleStoryContractRespond(f.callerCtx(), newCallToolReq("story_contract_respond", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"response_markdown":    "here is the answer",
	}))
	if err != nil || res.IsError {
		t.Fatalf("respond: err=%v text=%s", err, firstText(res))
	}
	var body struct {
		ResponseLedgerID string `json:"response_ledger_id"`
	}
	_ = json.Unmarshal([]byte(firstText(res)), &body)
	if body.ResponseLedgerID == "" {
		t.Fatalf("missing response_ledger_id")
	}
	row, err := f.server.ledger.GetByID(f.ctx, body.ResponseLedgerID, nil)
	if err != nil {
		t.Fatalf("load response: %v", err)
	}
	if row.Type != ledger.TypeDecision {
		t.Fatalf("type: %q", row.Type)
	}
	hasTag := false
	for _, tag := range row.Tags {
		if tag == "kind:review-response" {
			hasTag = true
		}
	}
	if !hasTag {
		t.Fatalf("missing kind:review-response tag: %v", row.Tags)
	}
}

func TestResume_RebindSameSessionClaimed(t *testing.T) {
	t.Parallel()
	f := newCloseFixture(t)
	f.claim(t, 0, "")
	// Register a new session and resume onto it.
	newSess := "resume-newsession"
	if _, err := f.server.sessions.Register(f.ctx, "user_alice", newSess, session.SourceSessionStart, f.now); err != nil {
		t.Fatalf("register: %v", err)
	}
	res, err := f.server.handleStoryContractResume(f.callerCtx(), newCallToolReq("story_contract_resume", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"session_id":           newSess,
		"reason":               "rebind",
	}))
	if err != nil || res.IsError {
		t.Fatalf("resume: err=%v text=%s", err, firstText(res))
	}
	ci, _ := f.server.contracts.GetByID(f.ctx, f.cis[0].ID, nil)
	if ci.ClaimedBySessionID != newSess {
		t.Fatalf("session not rebound: %q", ci.ClaimedBySessionID)
	}
	if ci.Status != contract.StatusClaimed {
		t.Fatalf("status changed: %q", ci.Status)
	}
}

func TestResume_ReopenPassedCI(t *testing.T) {
	t.Parallel()
	f := newCloseFixture(t)
	f.claim(t, 0, "initial plan")
	// Close CI[0] → passed.
	if res, _ := f.server.handleStoryContractClose(f.callerCtx(), newCallToolReq("story_contract_close", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"close_markdown":       "done",
	})); res.IsError {
		t.Fatalf("close[0]: %s", firstText(res))
	}
	// Claim + close CI[1] → passed (so downstream rollback has
	// something to flip).
	f.claim(t, 1, "")
	if res, _ := f.server.handleStoryContractClose(f.callerCtx(), newCallToolReq("story_contract_close", map[string]any{
		"contract_instance_id": f.cis[1].ID,
		"close_markdown":       "done",
	})); res.IsError {
		t.Fatalf("close[1]: %s", firstText(res))
	}
	// Capture CI[0]'s plan ledger id before resume.
	before, _ := f.server.contracts.GetByID(f.ctx, f.cis[0].ID, nil)
	priorPlan := before.PlanLedgerID

	res, err := f.server.handleStoryContractResume(f.callerCtx(), newCallToolReq("story_contract_resume", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"session_id":           f.sessionID,
		"reason":               "reopen to amend",
	}))
	if err != nil || res.IsError {
		t.Fatalf("resume: err=%v text=%s", err, firstText(res))
	}
	var body struct {
		Reopened      bool     `json:"reopened"`
		RolledBackCIs []string `json:"rolled_back_cis"`
	}
	_ = json.Unmarshal([]byte(firstText(res)), &body)
	if !body.Reopened {
		t.Fatalf("expected reopened=true: %s", firstText(res))
	}
	if len(body.RolledBackCIs) == 0 {
		t.Fatalf("expected downstream rollback, got none")
	}

	// CI[0] should be claimed again.
	after, _ := f.server.contracts.GetByID(f.ctx, f.cis[0].ID, nil)
	if after.Status != contract.StatusClaimed {
		t.Fatalf("CI[0] status: %q want claimed", after.Status)
	}
	// CI[1] should be back to ready.
	ci1, _ := f.server.contracts.GetByID(f.ctx, f.cis[1].ID, nil)
	if ci1.Status != contract.StatusReady {
		t.Fatalf("CI[1] not flipped back to ready: %q", ci1.Status)
	}
	// Prior plan should be dereferenced.
	if priorPlan != "" {
		planRow, err := f.server.ledger.GetByID(f.ctx, priorPlan, nil)
		if err != nil {
			t.Fatalf("load prior plan: %v", err)
		}
		if planRow.Status != ledger.StatusDereferenced {
			t.Fatalf("prior plan status: %q want dereferenced", planRow.Status)
		}
	}
}

func TestResume_CapPerCI(t *testing.T) {
	t.Parallel()
	f := newCloseFixture(t)
	f.claim(t, 0, "")

	// Drive the CI counter up to the cap by writing kv rows directly.
	for i := 1; i <= resumeCapCI; i++ {
		structured, _ := json.Marshal(map[string]any{"count": i})
		if _, err := f.server.ledger.Append(f.ctx, ledger.LedgerEntry{
			WorkspaceID: f.wsID,
			ProjectID:   f.projectID,
			StoryID:     ledger.StringPtr(f.storyID),
			Type:        ledger.TypeKV,
			Tags:        []string{"key:resume_count:ci:" + f.cis[0].ID},
			Content:     "count",
			Structured:  structured,
			CreatedBy:   "system",
		}, f.now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("seed counter %d: %v", i, err)
		}
	}

	res, err := f.server.handleStoryContractResume(f.callerCtx(), newCallToolReq("story_contract_resume", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"session_id":           f.sessionID,
		"reason":               "one too many",
	}))
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if !strings.Contains(firstText(res), `"error":"resume_cap_exceeded_ci"`) {
		t.Fatalf("expected resume_cap_exceeded_ci, got %s", firstText(res))
	}
}

func TestResume_CapPerStory(t *testing.T) {
	t.Parallel()
	f := newCloseFixture(t)
	f.claim(t, 0, "")

	for i := 1; i <= resumeCapStory; i++ {
		structured, _ := json.Marshal(map[string]any{"count": i})
		if _, err := f.server.ledger.Append(f.ctx, ledger.LedgerEntry{
			WorkspaceID: f.wsID,
			ProjectID:   f.projectID,
			StoryID:     ledger.StringPtr(f.storyID),
			Type:        ledger.TypeKV,
			Tags:        []string{"key:resume_count:story:" + f.storyID},
			Content:     "count",
			Structured:  structured,
			CreatedBy:   "system",
		}, f.now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("seed counter %d: %v", i, err)
		}
	}

	res, err := f.server.handleStoryContractResume(f.callerCtx(), newCallToolReq("story_contract_resume", map[string]any{
		"contract_instance_id": f.cis[0].ID,
		"session_id":           f.sessionID,
		"reason":               "story cap",
	}))
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if !strings.Contains(firstText(res), `"error":"resume_cap_exceeded_story"`) {
		t.Fatalf("expected resume_cap_exceeded_story, got %s", firstText(res))
	}
}

// Compile-time reference to avoid unused-import churn when these
// helpers exist only as convenience in tests.
var _ = context.Background
