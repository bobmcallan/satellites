//go:build portalui

package portalui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/story"
)

// TestAgents_EphemeralFilter (story_7b77ffb0 AC13) — chromedp drives
// the /agents page and toggles the canonical filter; only matching
// rows survive each pass.
func TestAgents_EphemeralFilter(t *testing.T) {
	h := StartHarness(t)
	now := time.Now().UTC()
	canonStruct, _ := document.MarshalAgentSettings(document.AgentSettings{})
	if _, err := h.Documents.Create(context.Background(), document.Document{
		WorkspaceID: h.WorkspaceID,
		Type:        document.TypeAgent, Scope: document.ScopeSystem, Status: "active",
		Name: "canonical_one", Body: "canonical agent", Structured: canonStruct,
	}, now); err != nil {
		t.Fatalf("seed canonical agent: %v", err)
	}
	storyID := "story_chromedp_eph"
	ephStruct, _ := document.MarshalAgentSettings(document.AgentSettings{
		Ephemeral: true,
		StoryID:   &storyID,
	})
	pid := "proj_chromedp"
	if _, err := h.Documents.Create(context.Background(), document.Document{
		WorkspaceID: h.WorkspaceID,
		Type:        document.TypeAgent, Scope: document.ScopeProject, Status: "active",
		Name: "ephemeral_one", Body: "ephemeral agent", Structured: ephStruct,
		ProjectID: &pid,
	}, now); err != nil {
		t.Fatalf("seed ephemeral agent: %v", err)
	}

	parent, cancel := withTimeout(context.Background(), browserDeadline)
	defer cancel()
	browserCtx, cancelBrowser := newChromedpContext(t, parent)
	defer cancelBrowser()
	if err := installSessionCookie(browserCtx, h); err != nil {
		t.Fatalf("install cookie: %v", err)
	}

	// Default: both rows visible.
	var bodyAll string
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(h.BaseURL+"/agents"),
		chromedp.WaitVisible(`[data-testid="agents-page"]`, chromedp.ByQuery),
		chromedp.OuterHTML("html", &bodyAll, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("/agents default: %v", err)
	}
	if !strings.Contains(bodyAll, "canonical_one") || !strings.Contains(bodyAll, "ephemeral_one") {
		t.Errorf("default /agents must show both agents; body=%s", bodyAll)
	}

	// canonical=true → only canonical_one survives.
	var bodyCanonical string
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(h.BaseURL+"/agents?canonical=true"),
		chromedp.WaitVisible(`[data-testid="agents-page"]`, chromedp.ByQuery),
		chromedp.OuterHTML("html", &bodyCanonical, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("/agents canonical=true: %v", err)
	}
	if !strings.Contains(bodyCanonical, "canonical_one") {
		t.Errorf("canonical filter must keep canonical_one")
	}
	if strings.Contains(bodyCanonical, "ephemeral_one") {
		t.Errorf("canonical filter must drop ephemeral_one")
	}

	// canonical=false → only ephemeral_one survives.
	var bodyEph string
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(h.BaseURL+"/agents?canonical=false"),
		chromedp.WaitVisible(`[data-testid="agents-page"]`, chromedp.ByQuery),
		chromedp.OuterHTML("html", &bodyEph, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("/agents canonical=false: %v", err)
	}
	if !strings.Contains(bodyEph, "ephemeral_one") {
		t.Errorf("ephemeral filter must keep ephemeral_one")
	}
	if strings.Contains(bodyEph, "canonical_one") {
		t.Errorf("ephemeral filter must drop canonical_one")
	}
}

// TestConfig_AgentsSectionRender (story_7b77ffb0 AC13) — confirms the
// agents section renders on /config under chromedp hydration.
func TestConfig_AgentsSectionRender(t *testing.T) {
	h := StartHarness(t)
	now := time.Now().UTC()
	settings, _ := document.MarshalAgentSettings(document.AgentSettings{
		PermissionPatterns: []string{"Read:**"},
	})
	if _, err := h.Documents.Create(context.Background(), document.Document{
		WorkspaceID: h.WorkspaceID,
		Type:        document.TypeAgent, Scope: document.ScopeSystem, Status: "active",
		Name: "developer_agent", Body: "developer", Structured: settings,
	}, now); err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	parent, cancel := withTimeout(context.Background(), browserDeadline)
	defer cancel()
	browserCtx, cancelBrowser := newChromedpContext(t, parent)
	defer cancelBrowser()
	if err := installSessionCookie(browserCtx, h); err != nil {
		t.Fatalf("install cookie: %v", err)
	}

	var body string
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(h.BaseURL+"/config"),
		chromedp.WaitVisible(`[data-testid="config-agents-panel"]`, chromedp.ByQuery),
		chromedp.OuterHTML("html", &body, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("/config agents section: %v", err)
	}
	if !strings.Contains(body, "developer_agent") {
		t.Errorf("/config agents panel missing preplan_agent")
	}
}

// TestStoryDetail_PlanTreeWithLoop (story_7b77ffb0 AC13) — seeds a
// story with a parent_invocation_id loop and asserts chromedp sees
// the child rendered after (and indented under) the parent.
func TestStoryDetail_PlanTreeWithLoop(t *testing.T) {
	h := StartHarness(t)
	now := time.Now().UTC()

	proj, err := h.Projects.Create(context.Background(), h.UserID, h.WorkspaceID, "tree-walk", now)
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	s, err := h.Stories.Create(context.Background(), story.Story{
		ProjectID: proj.ID, WorkspaceID: h.WorkspaceID,
		Title: "tree-walk fixture", Status: "in_progress",
		Priority: "high", Category: "feature", CreatedBy: h.UserID,
	}, now)
	if err != nil {
		t.Fatalf("seed story: %v", err)
	}
	contractDoc, err := h.Documents.Create(context.Background(), document.Document{
		WorkspaceID: h.WorkspaceID,
		Type:        document.TypeContract, Scope: document.ScopeSystem, Status: "active",
		Name: "develop", Body: "develop body",
	}, now)
	if err != nil {
		t.Fatalf("seed contract doc: %v", err)
	}
	parent, err := h.Contracts.Create(context.Background(), contract.ContractInstance{
		StoryID: s.ID, ContractID: contractDoc.ID, ContractName: "develop",
		Sequence: 2, Status: contract.StatusPassed,
	}, now)
	if err != nil {
		t.Fatalf("seed parent CI: %v", err)
	}
	_, err = h.Contracts.Create(context.Background(), contract.ContractInstance{
		StoryID: s.ID, ContractID: contractDoc.ID, ContractName: "develop",
		Sequence: 3, Status: contract.StatusReady,
		ParentInvocationID: parent.ID,
	}, now.Add(time.Second))
	if err != nil {
		t.Fatalf("seed child CI: %v", err)
	}

	parentCtx, cancel := withTimeout(context.Background(), browserDeadline)
	defer cancel()
	browserCtx, cancelBrowser := newChromedpContext(t, parentCtx)
	defer cancelBrowser()
	if err := installSessionCookie(browserCtx, h); err != nil {
		t.Fatalf("install cookie: %v", err)
	}

	var body string
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(h.BaseURL+"/projects/"+proj.ID+"/stories/"+s.ID),
		chromedp.WaitVisible(`[data-testid="ci-timeline-panel"]`, chromedp.ByQuery),
		chromedp.OuterHTML("html", &body, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("story detail: %v", err)
	}
	if !strings.Contains(body, `data-parent="`+parent.ID+`"`) {
		t.Errorf("child CI must carry data-parent referencing parent; body=%s", body)
	}
	if !strings.Contains(body, "ci-depth-1") {
		t.Errorf("child CI must render with ci-depth-1 class")
	}
}
