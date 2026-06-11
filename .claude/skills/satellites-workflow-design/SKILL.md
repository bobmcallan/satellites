---
name: satellites-workflow-design
type: skill
kind: capability
when: planning
tags: [kind:capability]
description: Design a story's ## Workflow from its requirement in isolated context — propose candidate state machines (states + gated transitions) using only the available gate skills, each justified, fail-closed. Invoked by `satellites workflow design`; the agent authors the workflow, the operator chooses.
---
<!-- satellites-sync:begin {"document_id":"doc_928f0d8d","version":2,"hash":"bdb15a0bc0da132b772d08880752a17c3ed6dbe7c1d3ee3253412513fa21dfd6"} satellites-sync:end -->

# Workflow design

Given a story's requirement, the available skills, and the fail-closed-gate
principle, propose the `## Workflow` state machine(s) for that requirement. You
design the lifecycle; the operator chooses. Do not implement the story.

## Input (on stdin, JSON)

```
{
  "requirement": "<the story body, requirement only>",
  "available_workflow_skills": [{"name": "...", "workflow": "<yaml states/transitions>"}],
  "available_gate_skills":     [{"name": "...", "description": "..."}],
  "fail_closed_gate_principle": "<principle body>"
}
```

## Rules

- **Use only the gate skills provided.** Every gated transition's `reviewer_skill`
  MUST be one of `available_gate_skills[].name`. Never invent a skill name.
- **Sound lifecycle.** An entry (initial) state, at least one terminal state (no
  outgoing edge), and — unless the work is trivial — an editable working phase
  between them (begin → work → end). No cycle that can never terminate.
- **Simplest fit.** Prefer the fewest states that serve the requirement. Reuse a
  canonical shape from `available_workflow_skills` when it fits; deviate only when
  needed, and say why in the rationale.
- **Gate where it matters** (per the fail-closed-gate principle): gate a transition
  that needs verification; a deliberate fast path may be ungated. Justify each.

## Output

Print STRICT JSON and nothing else:

```
{
  "recommended": <index into proposals>,
  "proposals": [
    {
      "rationale": "<one or two sentences: why this lifecycle fits the requirement>",
      "workflow": "```yaml\nstates:\n  - ...\ntransitions:\n  - {from: ..., to: ..., reviewer_skill: \"...\"}\n```"
    }
  ]
}
```

`workflow` MUST be a fenced ```yaml block containing `states` and `transitions`,
ready to drop into the story's `## Workflow` section. Offer 1–3 proposals (the
recommended one plus genuine alternatives when the requirement admits them).
