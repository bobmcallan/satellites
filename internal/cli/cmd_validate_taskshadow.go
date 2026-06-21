// Task-workflow shadow check for `satellites validate` (sty_d3fead0d). A repo
// workflow that declares `applies_to: [task]` but is NOT task-shaped (lacks the
// ready→running→complete lifecycle) outranks the canonical embed
// satellites-task-workflow for category:task (local+specific beats embed), so
// tasks fall into a story/deploy lifecycle whose gates can't apply — the
// 2026-06-22 cross-repo finding. work-artifact-selection names this a config gap
// to SURFACE; this makes `validate` surface it (a block finding → UNRESOLVABLE →
// non-zero exit), read-only — the operator drops `task` from its applies_to.
package cli

import (
	"strings"

	"github.com/bobmcallan/satellites/internal/workflow"
)

// checkTaskWorkflowShadows returns a block finding for every workflow candidate
// (the resolver's palette) that claims category:task but isn't task-shaped (so
// it shadows satellites-task-workflow). Read-only.
func checkTaskWorkflowShadows(wfs []matSkill) []driftFinding {
	var out []driftFinding
	for _, src := range wfs {
		// workflow.Parse (not ParseBody) reads the frontmatter applies_to AND the
		// ## Workflow states, so it needs the FULL file (raw); a malformed source
		// is skipped (the resolver's own checks surface that elsewhere).
		content := src.raw
		if strings.TrimSpace(content) == "" {
			content = src.body
		}
		wf, err := workflow.Parse([]byte(content))
		if err != nil || wf == nil {
			continue
		}
		if !appliesToTask(wf.AppliesTo) || taskShapedWorkflow(wf) {
			continue
		}
		out = append(out, driftFinding{
			Severity: "block",
			Code:     "task-workflow-shadow",
			Artifact: src.name,
			Message:  "claims applies_to:[task] but is not task-shaped (no ready→running→complete lifecycle) — it shadows the canonical satellites-task-workflow for category:task and routes tasks into a story/deploy lifecycle whose gates can't apply; drop \"task\" from its applies_to",
		})
	}
	return out
}

// appliesToTask reports whether an applies_to list governs the task category
// (an explicit "task"; the "*" wildcard is intentionally NOT treated as a task
// claim — a wildcard workflow is a deliberate catch-all, not a task-shaped one).
func appliesToTask(appliesTo []string) bool {
	for _, a := range appliesTo {
		if strings.EqualFold(strings.TrimSpace(a), "task") {
			return true
		}
	}
	return false
}

// taskShapedWorkflow reports whether a workflow carries the task lifecycle
// (ready → running → complete states) — the shape the task gates require.
func taskShapedWorkflow(wf *workflow.Workflow) bool {
	have := map[string]bool{}
	for _, s := range wf.States {
		have[strings.ToLower(strings.TrimSpace(s.Name))] = true
	}
	return have["ready"] && have["running"] && have["complete"]
}
