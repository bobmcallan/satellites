package mcpserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	satarbor "github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/reviewer"
	"github.com/bobmcallan/satellites/internal/session"
	"github.com/bobmcallan/satellites/internal/story"
	"github.com/bobmcallan/satellites/internal/workspace"
)

// capturingReviewer remembers the last Request it saw so tests can
// assert which rubric body the runReviewer dispatch resolved.
type capturingReviewer struct {
	last reviewer.Request
}

func (c *capturingReviewer) Review(_ context.Context, req reviewer.Request) (reviewer.Verdict, reviewer.UsageCost, error) {
	c.last = req
	return reviewer.Verdict{Outcome: reviewer.VerdictAccepted, Rationale: "ok"}, reviewer.UsageCost{}, nil
}

// dispatchFixture seeds a server with the two reviewer-agent docs at
// scope=system + a contract doc with validation_mode=llm + a CI for
// each contract name under test.
type dispatchFixture struct {
	ctx       context.Context
	server    *Server
	caller    CallerIdentity
	storyID   string
	wsID      string
	projectID string
	sessionID string
	rev       *capturingReviewer
	cis       map[string]contract.ContractInstance
	agents    map[string]string
	now       time.Time
}

func newDispatchFixture(t *testing.T) *dispatchFixture {
	t.Helper()
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	wsStore := workspace.NewMemoryStore()
	docStore := document.NewMemoryStore()
	ledStore := ledger.NewMemoryStore()
	storyStore := story.NewMemoryStore(ledStore)
	projStore := project.NewMemoryStore()
	contractStore := contract.NewMemoryStore(docStore, storyStore)
	sessionStore := session.NewMemoryStore()

	ws, err := wsStore.Create(ctx, "user_alice", "alpha", now)
	if err != nil {
		t.Fatalf("ws: %v", err)
	}
	proj, err := projStore.Create(ctx, "user_alice", ws.ID, "p1", now)
	if err != nil {
		t.Fatalf("project: %v", err)
	}

	// Reviewer agents at scope=system. Distinct bodies so the test
	// can verify which one the dispatch picked.
	for _, spec := range []struct {
		name string
		body string
	}{
		{"story_reviewer", "STORY_REVIEWER_RUBRIC_BODY"},
		{"development_reviewer", "DEVELOPMENT_REVIEWER_RUBRIC_BODY"},
	} {
		agentStructured, _ := document.MarshalAgentSettings(document.AgentSettings{
			PermissionPatterns: []string{"Read:**"},
		})
		if _, err := docStore.Create(ctx, document.Document{
			Type:       document.TypeAgent,
			Scope:      document.ScopeSystem,
			Status:     document.StatusActive,
			Name:       spec.name,
			Body:       spec.body,
			Structured: agentStructured,
		}, now); err != nil {
			t.Fatalf("seed %s: %v", spec.name, err)
		}
	}

	// Contract docs with validation_mode=llm so runReviewer takes the
	// LLM branch.
	structured, _ := json.Marshal(map[string]any{"validation_mode": reviewer.ModeLLM})
	contractDocs := map[string]document.Document{}
	for _, name := range []string{"develop", "preplan"} {
		d, err := docStore.Create(ctx, document.Document{
			Type:       document.TypeContract,
			Scope:      document.ScopeSystem,
			Name:       name,
			Status:     document.StatusActive,
			Body:       "body-" + name,
			Structured: structured,
		}, now)
		if err != nil {
			t.Fatalf("seed contract %s: %v", name, err)
		}
		contractDocs[name] = d
	}

	parent, err := storyStore.Create(ctx, story.Story{
		WorkspaceID: ws.ID,
		ProjectID:   proj.ID,
		Title:       "dispatch test",
	}, now)
	if err != nil {
		t.Fatalf("story: %v", err)
	}

	cis := map[string]contract.ContractInstance{}
	for i, name := range []string{"develop", "preplan"} {
		ci, err := contractStore.Create(ctx, contract.ContractInstance{
			StoryID:          parent.ID,
			ContractID:       contractDocs[name].ID,
			ContractName:     name,
			Sequence:         i,
			RequiredForClose: true,
			Status:           contract.StatusReady,
		}, now)
		if err != nil {
			t.Fatalf("ci seed %s: %v", name, err)
		}
		cis[name] = ci
	}

	// Lifecycle agents (strict-required by claim_handlers).
	agents := map[string]string{}
	for _, name := range []string{"develop", "preplan"} {
		agentStructured, _ := document.MarshalAgentSettings(document.AgentSettings{
			PermissionPatterns: []string{"Read:**"},
		})
		d, err := docStore.Create(ctx, document.Document{
			WorkspaceID: ws.ID,
			Type:        document.TypeAgent,
			Scope:       document.ScopeSystem,
			Status:      document.StatusActive,
			Name:        name + "_agent",
			Body:        "lifecycle agent for " + name,
			Structured:  agentStructured,
		}, now)
		if err != nil {
			t.Fatalf("seed lifecycle agent for %s: %v", name, err)
		}
		agents[name] = d.ID
	}

	rev := &capturingReviewer{}
	server := New(&config.Config{Env: "dev"}, satarbor.New("info"), now, Deps{
		DocStore:         docStore,
		ProjectStore:     projStore,
		DefaultProjectID: proj.ID,
		LedgerStore:      ledStore,
		StoryStore:       storyStore,
		WorkspaceStore:   wsStore,
		ContractStore:    contractStore,
		SessionStore:     sessionStore,
		Reviewer:         rev,
		NowFunc:          func() time.Time { return now },
	})
	sessionID := "session-dispatch-test"
	if _, err := sessionStore.Register(ctx, "user_alice", sessionID, session.SourceSessionStart, now); err != nil {
		t.Fatalf("register session: %v", err)
	}
	return &dispatchFixture{
		ctx:       ctx,
		server:    server,
		caller:    CallerIdentity{UserID: "user_alice", Source: "session"},
		storyID:   parent.ID,
		wsID:      ws.ID,
		projectID: proj.ID,
		sessionID: sessionID,
		rev:       rev,
		cis:       cis,
		agents:    agents,
		now:       now,
	}
}

func (f *dispatchFixture) callerCtx() context.Context {
	return withCaller(f.ctx, f.caller)
}

func (f *dispatchFixture) closeCI(t *testing.T, name string) {
	t.Helper()
	ci := f.cis[name]
	if _, err := f.server.handleContractClaim(f.callerCtx(), newCallToolReq("contract_claim", map[string]any{
		"contract_instance_id": ci.ID,
		"session_id":           f.sessionID,
		"agent_id":             f.agents[name],
	})); err != nil {
		t.Fatalf("claim %s: %v", name, err)
	}
	if _, err := f.server.handleContractClose(f.callerCtx(), newCallToolReq("contract_close", map[string]any{
		"contract_instance_id": ci.ID,
		"close_markdown":       "done",
		"evidence_markdown":    "evidence-" + name,
	})); err != nil {
		t.Fatalf("close %s: %v", name, err)
	}
}

func TestRunReviewer_DispatchDevelopUsesDevelopmentReviewer(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	f.closeCI(t, "develop")
	if got, want := f.rev.last.ReviewerRubric, "DEVELOPMENT_REVIEWER_RUBRIC_BODY"; got != want {
		t.Errorf("develop ReviewerRubric = %q, want %q", got, want)
	}
	if got, want := f.rev.last.ContractName, "develop"; got != want {
		t.Errorf("develop ContractName = %q, want %q", got, want)
	}
}

func TestRunReviewer_DispatchNonDevelopUsesStoryReviewer(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	f.closeCI(t, "preplan")
	if got, want := f.rev.last.ReviewerRubric, "STORY_REVIEWER_RUBRIC_BODY"; got != want {
		t.Errorf("preplan ReviewerRubric = %q, want %q", got, want)
	}
	if got, want := f.rev.last.ContractName, "preplan"; got != want {
		t.Errorf("preplan ContractName = %q, want %q", got, want)
	}
}
