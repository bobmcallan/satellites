// project-config parsing. The server-side story_request_review driver that
// once owned this file was retired (sty_6e1c3641) — workflow dispatch is the
// dynamic skill index (sty_815c09e7) and the reviewer gate runs client-side
// (satellites story review). What survives is the typed project-config shape +
// parser, kept here because the client gate's step-summariser lookup
// (reviewStepSummariserSkill) parses project-config through this one owner.

package verb

import (
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/bobmcallan/satellites/internal/workflow"
)

// ProjectConfig is the residual project-config shape. story_types and
// reviewer_overrides were retired (sty_815c09e7: dispatch is index-derived);
// what remains is the optional post-transition step-summariser setting.
type ProjectConfig struct {
	// StoryTypes is retained for backward-compatible parsing of older
	// configs; it is no longer read on the dispatch path (the dynamic skill
	// index's applies_to is the single source).
	StoryTypes map[string]StoryTypeConfig `yaml:"story_types"`
	// StepSummariserSkill names the skill the loop runs after each gated
	// transition to produce a human-readable step summary (sty_2517f6b8).
	// Empty disables summaries. Resolves to .claude/skills/<name>/SKILL.md.
	StepSummariserSkill string `yaml:"step_summariser_skill"`
}

// StoryTypeConfig is one legacy story_type → workflow-skill mapping (parsed
// for backward compatibility; not read on the dispatch path).
type StoryTypeConfig struct {
	WorkflowSkill string `yaml:"workflow_skill"`
}

// ParseProjectConfig turns a project-config document body into the typed
// config. The body is YAML inside a markdown ```yaml fence (so humans can
// annotate around it), so we extract the fenced block before unmarshalling
// — the same fence-vs-raw class of bug the workflow loader already handles
// (sty_8da63e77). A body with no fence is treated as raw YAML so legacy
// fence-less configs still parse. Exported so the client-side reviewer
// command parses project-config through this one owner.
//
// story_types is not required: workflow dispatch is index-derived
// (sty_815c09e7), so a slim project-config carrying only residual settings
// (e.g. step_summariser_skill) and no story_types is valid.
func ParseProjectConfig(body string) (ProjectConfig, error) {
	raw := []byte(body)
	if block, err := workflow.ExtractYAMLBlock(raw); err == nil {
		raw = block
	}
	var cfg ProjectConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return ProjectConfig{}, fmt.Errorf("parse project-config yaml: %w", err)
	}
	return cfg, nil
}
