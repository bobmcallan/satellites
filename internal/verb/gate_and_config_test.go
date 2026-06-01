package verb

import (
	"strings"
	"testing"
)

// --- gate output parser (request_review_dispatcher.go) ---

func TestParseGateOutput_AcceptPlain(t *testing.T) {
	out, err := ParseGateOutput([]byte(`{"decision":"accept","notes":"ok"}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out.Decision != GateDecisionAccept || out.Notes != "ok" {
		t.Fatalf("out = %+v", out)
	}
}

func TestParseGateOutput_WrappedFence(t *testing.T) {
	out, err := ParseGateOutput([]byte("```json\n{\"decision\":\"reject\",\"notes\":\"nope\"}\n```\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out.Decision != GateDecisionReject || out.Notes != "nope" {
		t.Fatalf("out = %+v", out)
	}
}

func TestParseGateOutput_PreambleAndJSON(t *testing.T) {
	out, err := ParseGateOutput([]byte("Some preamble.\n\n{\"decision\":\"accept\",\"next_status\":\"complex\"}"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out.Decision != GateDecisionAccept {
		t.Fatalf("decision = %q", out.Decision)
	}
	if out.NextStatus != "complex" {
		t.Fatalf("next_status = %q, want complex", out.NextStatus)
	}
}

func TestParseGateOutput_InvalidDecision(t *testing.T) {
	_, err := ParseGateOutput([]byte(`{"decision":"maybe"}`))
	if err == nil || !strings.Contains(err.Error(), "invalid decision") {
		t.Fatalf("expected invalid-decision error, got %v", err)
	}
}

func TestParseGateOutput_Malformed(t *testing.T) {
	_, err := ParseGateOutput([]byte(`not json at all`))
	if err == nil || !strings.Contains(err.Error(), "parse output") {
		t.Fatalf("expected parse error, got %v", err)
	}
}

// --- project-config parser (projectconfig.go) ---

func TestParseProjectConfig_FencedBody(t *testing.T) {
	body := "# project-config (satellites)\n" +
		"#\n" +
		"# Project-scoped configuration. Body is YAML inside a markdown fence.\n" +
		"\n" +
		"```yaml\n" +
		"story_types:\n" +
		"  feature:\n" +
		"    workflow_skill: .claude/skills/feature-workflow.md\n" +
		"  fix:\n" +
		"    workflow_skill: .claude/skills/fix-workflow.md\n" +
		"reviewer_overrides: {}\n" +
		"```\n" +
		"\n" +
		"## Notes\n\nFree-form prose after the block must be ignored.\n"

	cfg, err := ParseProjectConfig(body)
	if err != nil {
		t.Fatalf("ParseProjectConfig(fenced body): %v", err)
	}
	if got := cfg.StoryTypes["feature"].WorkflowSkill; got != ".claude/skills/feature-workflow.md" {
		t.Fatalf("feature workflow_skill = %q", got)
	}
	if got := cfg.StoryTypes["fix"].WorkflowSkill; got != ".claude/skills/fix-workflow.md" {
		t.Fatalf("fix workflow_skill = %q", got)
	}
}

func TestParseProjectConfig_StepSummariserSkill(t *testing.T) {
	with := "```yaml\nstory_types:\n  feature:\n    workflow_skill: .claude/skills/feature-workflow/SKILL.md\nstep_summariser_skill: story_summary\n```\n"
	cfg, err := ParseProjectConfig(with)
	if err != nil {
		t.Fatalf("ParseProjectConfig: %v", err)
	}
	if cfg.StepSummariserSkill != "story_summary" {
		t.Fatalf("StepSummariserSkill = %q, want story_summary", cfg.StepSummariserSkill)
	}

	without := "```yaml\nstory_types:\n  feature:\n    workflow_skill: .claude/skills/feature-workflow/SKILL.md\n```\n"
	cfg2, err := ParseProjectConfig(without)
	if err != nil {
		t.Fatalf("ParseProjectConfig (no summariser): %v", err)
	}
	if cfg2.StepSummariserSkill != "" {
		t.Fatalf("StepSummariserSkill = %q, want empty when unconfigured", cfg2.StepSummariserSkill)
	}
}

// TestParseProjectConfig_SlimNoStoryTypes pins sty_815c09e7: workflow dispatch
// is index-derived, so a slim project-config with no story_types — carrying
// only the residual step_summariser_skill — parses cleanly.
func TestParseProjectConfig_SlimNoStoryTypes(t *testing.T) {
	slim := "```yaml\nstep_summariser_skill: story_summary\n```\n"
	cfg, err := ParseProjectConfig(slim)
	if err != nil {
		t.Fatalf("ParseProjectConfig(slim, no story_types): %v", err)
	}
	if len(cfg.StoryTypes) != 0 {
		t.Fatalf("StoryTypes = %v, want empty", cfg.StoryTypes)
	}
	if cfg.StepSummariserSkill != "story_summary" {
		t.Fatalf("StepSummariserSkill = %q, want story_summary", cfg.StepSummariserSkill)
	}
}

func TestParseProjectConfig_RawBody(t *testing.T) {
	body := "story_types:\n  feature:\n    workflow_skill: .claude/skills/feature-workflow.md\n"
	cfg, err := ParseProjectConfig(body)
	if err != nil {
		t.Fatalf("ParseProjectConfig(raw body): %v", err)
	}
	if got := cfg.StoryTypes["feature"].WorkflowSkill; got != ".claude/skills/feature-workflow.md" {
		t.Fatalf("feature workflow_skill = %q", got)
	}
}

// TestParseProjectConfig_NoStoryTypes pins the post-sty_815c09e7 contract:
// workflow dispatch is index-derived, so a config with no story_types parses
// cleanly with an empty StoryTypes map (a consumer needing a mapping fails at
// its own lookup, not at parse).
func TestParseProjectConfig_NoStoryTypes(t *testing.T) {
	body := "```yaml\nreviewer_overrides: {}\n```\n"
	cfg, err := ParseProjectConfig(body)
	if err != nil {
		t.Fatalf("slim config should parse, got error: %v", err)
	}
	if len(cfg.StoryTypes) != 0 {
		t.Fatalf("StoryTypes = %v, want empty", cfg.StoryTypes)
	}
}
