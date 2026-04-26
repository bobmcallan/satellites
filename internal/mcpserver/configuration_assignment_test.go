package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/story"
)

// seedConfiguration creates a type=configuration document in the
// fixture's workspace whose ContractRefs reference the seeded
// contract docs by name. Returns the document id.
func (f *contractFixture) seedConfiguration(t *testing.T, name string, contractNames []string) string {
	t.Helper()
	docs, err := f.server.docs.List(f.ctx, document.ListOptions{Type: document.TypeContract}, nil)
	if err != nil {
		t.Fatalf("list contract docs: %v", err)
	}
	byName := make(map[string]string, len(docs))
	for _, d := range docs {
		byName[d.Name] = d.ID
	}
	refs := make([]string, 0, len(contractNames))
	for _, cn := range contractNames {
		id, ok := byName[cn]
		if !ok {
			t.Fatalf("seedConfiguration: contract %q not seeded", cn)
		}
		refs = append(refs, id)
	}
	payload, err := document.MarshalConfiguration(document.Configuration{ContractRefs: refs})
	if err != nil {
		t.Fatalf("MarshalConfiguration: %v", err)
	}
	cfgDoc, err := f.server.docs.Create(f.ctx, document.Document{
		WorkspaceID: f.wsID,
		ProjectID:   document.StringPtr(f.projectID),
		Type:        document.TypeConfiguration,
		Name:        name,
		Body:        name + " bundle",
		Scope:       document.ScopeProject,
		Structured:  payload,
	}, f.now)
	if err != nil {
		t.Fatalf("Create configuration: %v", err)
	}
	return cfgDoc.ID
}

func TestHandleStoryCreate_ConfigurationID_Valid(t *testing.T) {
	t.Parallel()
	f := newContractFixture(t)
	cfgID := f.seedConfiguration(t, "frontend", []string{"preplan", "plan", "develop", "story_close"})

	res, err := f.server.handleStoryCreate(f.callerCtx(), newCallToolReq("story_create", map[string]any{
		"project_id":       f.projectID,
		"title":            "with-cfg",
		"configuration_id": cfgID,
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("isError: %s", firstText(res))
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(firstText(res)), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got, _ := body["configuration_id"].(string); got != cfgID {
		t.Errorf("configuration_id = %q, want %q", got, cfgID)
	}
	if got, _ := body["configuration_name"].(string); got != "frontend" {
		t.Errorf("configuration_name = %q, want %q", got, "frontend")
	}
}

func TestHandleStoryCreate_ConfigurationID_DanglingRejected(t *testing.T) {
	t.Parallel()
	f := newContractFixture(t)
	res, err := f.server.handleStoryCreate(f.callerCtx(), newCallToolReq("story_create", map[string]any{
		"project_id":       f.projectID,
		"title":            "bad-cfg",
		"configuration_id": "doc_does_not_exist",
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected isError; got %s", firstText(res))
	}
	if !strings.Contains(firstText(res), "doc_does_not_exist") {
		t.Errorf("error must name the missing id; got %q", firstText(res))
	}
}

func TestHandleStoryCreate_ConfigurationID_WrongTypeRejected(t *testing.T) {
	t.Parallel()
	f := newContractFixture(t)
	// Seed a project-scope contract document in the test workspace so the
	// membership check passes and the type-mismatch branch fires. Using a
	// system-scope contract here would (correctly) be rejected at the
	// access layer first, masking the type error this test cares about.
	wrongDoc, err := f.server.docs.Create(f.ctx, document.Document{
		WorkspaceID: f.wsID,
		ProjectID:   document.StringPtr(f.projectID),
		Type:        document.TypeContract,
		Scope:       document.ScopeProject,
		Name:        "in-ws-contract",
		Body:        "body",
	}, f.now)
	if err != nil {
		t.Fatalf("seed wrong-type doc: %v", err)
	}
	res, err := f.server.handleStoryCreate(f.callerCtx(), newCallToolReq("story_create", map[string]any{
		"project_id":       f.projectID,
		"title":            "wrong-type",
		"configuration_id": wrongDoc.ID,
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected isError; got %s", firstText(res))
	}
	if !strings.Contains(firstText(res), "want type=configuration") {
		t.Errorf("error must name the expected type; got %q", firstText(res))
	}
}

func TestHandleStoryUpdate_ConfigurationID_RoundTrip(t *testing.T) {
	t.Parallel()
	f := newContractFixture(t)
	cfgID := f.seedConfiguration(t, "round", []string{"preplan", "plan", "develop", "story_close"})

	// Set
	setRes, err := f.server.handleStoryUpdate(f.callerCtx(), newCallToolReq("story_update", map[string]any{
		"id":               f.storyID,
		"configuration_id": cfgID,
	}))
	if err != nil {
		t.Fatalf("set err: %v", err)
	}
	if setRes.IsError {
		t.Fatalf("set isError: %s", firstText(setRes))
	}
	var afterSet map[string]any
	_ = json.Unmarshal([]byte(firstText(setRes)), &afterSet)
	if got, _ := afterSet["configuration_id"].(string); got != cfgID {
		t.Errorf("after set: configuration_id = %q, want %q", got, cfgID)
	}

	// Clear via empty string
	clrRes, err := f.server.handleStoryUpdate(f.callerCtx(), newCallToolReq("story_update", map[string]any{
		"id":               f.storyID,
		"configuration_id": "",
	}))
	if err != nil {
		t.Fatalf("clear err: %v", err)
	}
	var afterClr map[string]any
	_ = json.Unmarshal([]byte(firstText(clrRes)), &afterClr)
	if _, present := afterClr["configuration_id"]; present {
		// "omitempty" should drop the nil pointer; presence is a regression.
		t.Errorf("after clear: configuration_id present in response; expected omitted")
	}
}

func TestHandleStoryWorkflowClaim_ConfigurationOverride_EmptyProposed(t *testing.T) {
	t.Parallel()
	f := newContractFixture(t)
	// Configuration uses a 4-slot order: preplan → plan → develop → story_close.
	cfgID := f.seedConfiguration(t, "alpha", []string{"preplan", "plan", "develop", "story_close"})
	// Assign configuration to the parent story via the store.
	cid := cfgID
	if _, err := f.server.stories.Update(f.ctx, f.storyID, story.UpdateFields{ConfigurationID: &cid}, "test", f.now, nil); err != nil {
		t.Fatalf("assign cfg: %v", err)
	}

	res, err := f.server.handleStoryWorkflowClaim(f.callerCtx(), newCallToolReq("story_workflow_claim", map[string]any{
		"story_id":       f.storyID,
		"claim_markdown": "claim from configuration",
		// proposed_contracts intentionally omitted
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("isError: %s", firstText(res))
	}
	var body struct {
		ContractInstances []contract.ContractInstance `json:"contract_instances"`
	}
	if err := json.Unmarshal([]byte(firstText(res)), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.ContractInstances) != 4 {
		t.Fatalf("CI count = %d, want 4 (matches Configuration's 4-ref list)", len(body.ContractInstances))
	}
	want := []string{"preplan", "plan", "develop", "story_close"}
	for i, ci := range body.ContractInstances {
		if ci.ContractName != want[i] {
			t.Errorf("CI[%d] = %q, want %q", i, ci.ContractName, want[i])
		}
	}
}

func TestHandleStoryWorkflowClaim_ProposedWinsOverConfiguration(t *testing.T) {
	t.Parallel()
	f := newContractFixture(t)
	cfgID := f.seedConfiguration(t, "alpha", []string{"preplan", "plan", "develop", "story_close"})
	cid := cfgID
	if _, err := f.server.stories.Update(f.ctx, f.storyID, story.UpdateFields{ConfigurationID: &cid}, "test", f.now, nil); err != nil {
		t.Fatalf("assign cfg: %v", err)
	}

	// Provide a proposed list explicitly — should win over Configuration.
	// Includes plan because the project's default workflow_spec marks
	// plan as required (min_count=1).
	res, err := f.server.handleStoryWorkflowClaim(f.callerCtx(), newCallToolReq("story_workflow_claim", map[string]any{
		"story_id":           f.storyID,
		"proposed_contracts": []string{"preplan", "plan", "develop", "develop", "story_close"},
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("isError: %s", firstText(res))
	}
	var body struct {
		ContractInstances []contract.ContractInstance `json:"contract_instances"`
	}
	_ = json.Unmarshal([]byte(firstText(res)), &body)
	if len(body.ContractInstances) != 5 {
		t.Fatalf("CI count = %d, want 5 (proposed override)", len(body.ContractInstances))
	}
}

func TestHandleStoryWorkflowClaim_NullConfiguration_FallsBackToProjectDefault(t *testing.T) {
	t.Parallel()
	f := newContractFixture(t)
	// No configuration assigned; no proposed_contracts; should expand
	// from project workflow_spec default (preplan/plan/develop/story_close).
	res, err := f.server.handleStoryWorkflowClaim(f.callerCtx(), newCallToolReq("story_workflow_claim", map[string]any{
		"story_id": f.storyID,
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("isError: %s", firstText(res))
	}
	var body struct {
		ContractInstances []contract.ContractInstance `json:"contract_instances"`
	}
	_ = json.Unmarshal([]byte(firstText(res)), &body)
	if len(body.ContractInstances) != 4 {
		t.Fatalf("CI count = %d, want 4 (default spec)", len(body.ContractInstances))
	}
}

func TestHandleDocumentDelete_ConfigurationReferencedByStory_Rejected(t *testing.T) {
	t.Parallel()
	f := newContractFixture(t)
	cfgID := f.seedConfiguration(t, "alpha", []string{"preplan", "plan", "develop", "story_close"})
	cid := cfgID
	if _, err := f.server.stories.Update(f.ctx, f.storyID, story.UpdateFields{ConfigurationID: &cid}, "test", f.now, nil); err != nil {
		t.Fatalf("assign cfg: %v", err)
	}

	res, err := f.server.handleDocumentDelete(f.callerCtx(), newCallToolReq("document_delete", map[string]any{
		"id": cfgID,
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected delete to be blocked; got %s", firstText(res))
	}
	text := firstText(res)
	if !strings.Contains(text, f.storyID) {
		t.Errorf("error must list referencing story id %q; got %q", f.storyID, text)
	}
}

func TestHandleDocumentDelete_ConfigurationReferencedByDoneStory_Allowed(t *testing.T) {
	t.Parallel()
	f := newContractFixture(t)
	cfgID := f.seedConfiguration(t, "alpha", []string{"preplan", "plan", "develop", "story_close"})
	cid := cfgID
	if _, err := f.server.stories.Update(f.ctx, f.storyID, story.UpdateFields{ConfigurationID: &cid}, "test", f.now, nil); err != nil {
		t.Fatalf("assign cfg: %v", err)
	}
	// Move story through ready → in_progress → done so the FK gate sees a closed referrer.
	for _, target := range []string{story.StatusReady, story.StatusInProgress, story.StatusDone} {
		if _, err := f.server.stories.UpdateStatus(f.ctx, f.storyID, target, "test", f.now.Add(time.Minute), nil); err != nil {
			t.Fatalf("transition to %s: %v", target, err)
		}
	}

	res, err := f.server.handleDocumentDelete(f.callerCtx(), newCallToolReq("document_delete", map[string]any{
		"id": cfgID,
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("done-story should not block delete; got %s", firstText(res))
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(firstText(res)), &body)
	if got, _ := body["deleted"].(bool); !got {
		t.Errorf("expected deleted=true; body=%v", body)
	}
}
