package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workflow"
)

// TestReviewerKeyTTLOutlivesDispatch pins sty_64c6159f: the minted reviewer
// key must live longer than the gate dispatch it is minted for. A gate run
// (build + test) can take up to gateDispatchTimeout; if the key expires
// first, the gate's post-decision spine writes (review_accept/reject +
// status_transition) 401 on the dead key and the verdict's ledger trail is
// lost. The TTL is derived from the timeout + headroom so the two cannot
// drift; this test fails if a future edit lets the TTL fall to or below the
// dispatch timeout.
func TestReviewerKeyTTLOutlivesDispatch(t *testing.T) {
	if reviewerKeyTTL <= gateDispatchTimeout {
		t.Fatalf("reviewerKeyTTL (%s) must exceed gateDispatchTimeout (%s) so a long gate run can still write its spine rows",
			reviewerKeyTTL, gateDispatchTimeout)
	}
	if headroom := reviewerKeyTTL - gateDispatchTimeout; headroom < time.Minute {
		t.Fatalf("reviewerKeyTTL headroom over the dispatch timeout is only %s; want at least 1m of slack", headroom)
	}
}

// TestClientResolvesWorkflowFromSkillMdPath pins sty_cce5abc0 AC2 (client
// path): `satellites story review` resolves the workflow via
// verb.ParseProjectConfig (story_type → workflow_skill path) and parses the
// SKILL.md at that path with workflow.Parse. This test exercises that exact
// chain against a project-config that points at the new `<name>/SKILL.md`
// dir form and the live feature-workflow skill — proving the client loads +
// parses a workflow skill from the SKILL.md path, not the retired flat file.
func TestClientResolvesWorkflowFromSkillMdPath(t *testing.T) {
	const projectConfig = "```yaml\n" +
		"story_types:\n" +
		"  feature:\n" +
		"    workflow_skill: .claude/skills/satellites-feature-workflow/SKILL.md\n" +
		"```\n"

	cfg, err := verb.ParseProjectConfig(projectConfig)
	if err != nil {
		t.Fatalf("ParseProjectConfig: %v", err)
	}
	st, ok := cfg.StoryTypes["feature"]
	if !ok {
		t.Fatal("project-config has no feature story_type")
	}
	want := ".claude/skills/satellites-feature-workflow/SKILL.md"
	if st.WorkflowSkill != want {
		t.Fatalf("workflow_skill = %q, want the SKILL.md dir path %q", st.WorkflowSkill, want)
	}

	// Read + parse the live skill from that path (worktree root is two up
	// from internal/cli), the same os.ReadFile → workflow.Parse the client
	// performs in reviewWorkflowSkillPath's caller.
	raw, err := os.ReadFile(filepath.Join("..", "..", st.WorkflowSkill))
	if err != nil {
		t.Fatalf("read workflow skill at resolved path: %v", err)
	}
	wf, err := workflow.Parse(raw)
	if err != nil {
		t.Fatalf("workflow.Parse: %v", err)
	}
	if wf.Name != "satellites-feature-workflow" {
		t.Fatalf("parsed workflow name = %q, want satellites-feature-workflow", wf.Name)
	}
	if _, ok := wf.FindTransition("in_progress", "done"); !ok {
		t.Fatal("expected in_progress→done transition in the resolved workflow")
	}
}

// TestResolveReviewTransition pins the agent-driven gate selector
// (sty_bd6a4f53): no --skill falls back to the status-derived rule; a named
// skill must be a reviewer_skill that leaves the current status, else the
// request is rejected (error) before any gate dispatch.
func TestResolveReviewTransition(t *testing.T) {
	wf := &workflow.Workflow{
		Name:      "fixwf",
		AppliesTo: []string{"fix"},
		States:    []string{"backlog", "in_progress", "done"},
		Transitions: []workflow.Transition{
			{From: "backlog", To: "in_progress", ReviewerSkill: "plan-review"},
			{From: "in_progress", To: "done", ReviewerSkill: "done-review"},
		},
	}
	// No skill -> status-derived (PickTransition).
	if tr, gs, _, err := resolveReviewTransition(wf, "backlog", ""); err != nil || gs != "plan-review" || tr.To != "in_progress" {
		t.Fatalf("status-derived: got (to=%q, skill=%q, err=%v)", tr.To, gs, err)
	}
	// Named skill that leaves the current status -> that transition.
	if tr, gs, _, err := resolveReviewTransition(wf, "in_progress", "done-review"); err != nil || gs != "done-review" || tr.To != "done" {
		t.Fatalf("named skill: got (to=%q, skill=%q, err=%v)", tr.To, gs, err)
	}
	// Unknown/unimplemented skill -> reject.
	if _, _, _, err := resolveReviewTransition(wf, "backlog", "story_close"); err == nil {
		t.Fatal("unknown skill should be rejected")
	}
	// Known skill but not from the current status -> reject (status mismatch).
	if _, _, _, err := resolveReviewTransition(wf, "backlog", "done-review"); err == nil {
		t.Fatal("status-mismatch skill should be rejected")
	}
}
