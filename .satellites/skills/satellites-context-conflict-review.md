---
name: satellites-context-conflict-review
type: skill
kind: capability
scope: project
when: review
tags: [kind:capability]
description: Semantic reviewer for the assembled delivered context — given the project principles, the skills index, and a story's ## Workflow, judge whether they CONFLICT (contradictory principles, a principle a skill or the workflow cannot honour, a required step that is not a gated transition) and emit findings as JSON. Invoked by `satellites context review --semantic`; the structural layer is order:3.
---

# Context-conflict review (semantic layer)

Read the assembled delivered context and report the judgement-requiring
CONFLICTS — the ones that need reasoning, not a parser. The structural layer
(degenerate lifecycle, missing gate skill, unparseable workflow) is checked
deterministically elsewhere and is NOT your job. Do not implement, suggest fixes,
or rewrite. Report conflicts; when the bundle is coherent, report none.

## Input (on stdin, JSON)

```
{
  "principles": [{"name": "...", "body": "..."}],
  "skills":     [{"name": "...", "kind": "...", "description": "..."}],
  "workflow":   "## Workflow\n```yaml\n...\n```"
}
```

## Conflict codes

- `contradictory-principles` — two principles that cannot both be honoured (one
  requires X, another forbids X). Quote both.
- `principle-skill-conflict` — a skill's described behaviour violates a principle.
  Name the skill and the principle.
- `required-step-not-gated` — a principle or skill requires a step that the story's
  `## Workflow` does not express as a gated transition.
- `principle-unhonourable-by-workflow` — a principle the assembled `## Workflow`
  structurally cannot honour (e.g. a fail-closed principle, but an unguarded edge
  into a terminal state).

Only report a conflict you can justify by quoting the conflicting parties. Be
conservative — false conflicts erode trust. An empty `findings` array is the
correct, common answer for a coherent bundle.

## Output (stdout, JSON ONLY — no prose, no code fence)

```
{"findings": [
  {"severity": "error|warn", "code": "<one of the four codes>", "message": "<what conflicts, quoting both parties>"}
]}
```

Emit `{"findings": []}` when there is no conflict. Output the JSON object and
nothing else.
