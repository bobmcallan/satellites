package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/document"
)

// TestWorkflowClaim_ResolverNamesProjectSource covers AC3 of
// story_f0a78759: when the resolved scope-mandate stack adds a slot
// at a lower tier (project here) and the proposed_contracts omits it,
// the structured error response must carry source="project" so the
// caller can attribute the rejection to the originating layer.
func TestWorkflowClaim_ResolverNamesProjectSource(t *testing.T) {
	t.Parallel()
	f := newContractFixture(t)

	// Seed a system workflow Document with the four-slot default and a
	// project-tier workflow Document that adds a `compliance_review`
	// requirement. The mcp resolver merges them into the resolved spec
	// the workflow_claim gate enforces.
	systemSrc := document.Document{
		Type:   document.TypeWorkflow,
		Scope:  document.ScopeSystem,
		Name:   "default",
		Body:   "system workflow",
		Status: document.StatusActive,
		Structured: []byte(`{"required_slots":[
			{"contract_name":"preplan","required":true,"min_count":1,"max_count":1},
			{"contract_name":"plan","required":true,"min_count":1,"max_count":1},
			{"contract_name":"develop","required":true,"min_count":1,"max_count":10},
			{"contract_name":"story_close","required":true,"min_count":1,"max_count":1}
		]}`),
	}
	if _, err := f.server.docs.Create(context.Background(), systemSrc, f.now); err != nil {
		t.Fatalf("seed system workflow: %v", err)
	}

	pid := f.projectID
	projectSrc := document.Document{
		Type:        document.TypeWorkflow,
		Scope:       document.ScopeProject,
		Name:        "project_compliance",
		Body:        "project workflow override",
		Status:      document.StatusActive,
		WorkspaceID: f.wsID,
		ProjectID:   &pid,
		Structured: []byte(`{"required_slots":[
			{"contract_name":"compliance_review","required":true,"min_count":1,"max_count":1}
		]}`),
	}
	if _, err := f.server.docs.Create(context.Background(), projectSrc, f.now); err != nil {
		t.Fatalf("seed project workflow: %v", err)
	}

	// Also seed the compliance_review contract document so it resolves
	// to a docID — though we won't reach the resolution step because
	// the proposed list omits it and the spec gate trips first.
	if _, err := f.server.docs.Create(context.Background(), document.Document{
		Type:   document.TypeContract,
		Scope:  document.ScopeSystem,
		Name:   "compliance_review",
		Body:   "compliance contract",
		Status: document.StatusActive,
	}, f.now); err != nil {
		t.Fatalf("seed compliance contract: %v", err)
	}

	res, err := f.server.handleWorkflowClaim(f.callerCtx(), newCallToolReq("workflow_claim", map[string]any{
		"story_id":           f.storyID,
		"proposed_contracts": []string{"preplan", "plan", "develop", "story_close"},
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	text := firstText(res)
	if !strings.Contains(text, `"error":"missing_required_slot"`) {
		t.Fatalf("expected missing_required_slot, got %s", text)
	}
	if !strings.Contains(text, `"contract_name":"compliance_review"`) {
		t.Fatalf("expected compliance_review name, got %s", text)
	}
	if !strings.Contains(text, `"source":"project"`) {
		t.Fatalf("expected source=project, got %s", text)
	}
}

// TestLoadResolvedWorkflowSpec_FallbackToDefault confirms the fallback
// path: when no workflow Document is seeded at any scope, the loader
// returns the project KV row + DefaultWorkflowSpec().
func TestLoadResolvedWorkflowSpec_FallbackToDefault(t *testing.T) {
	t.Parallel()
	f := newContractFixture(t)
	spec, err := f.server.loadResolvedWorkflowSpec(f.ctx, f.wsID, f.projectID, f.caller.UserID, nil)
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	if len(spec.Slots) != 4 {
		t.Fatalf("default slot count: got %d want 4 (preplan/plan/develop/story_close)", len(spec.Slots))
	}
}
