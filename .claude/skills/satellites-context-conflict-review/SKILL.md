<!-- satellites-sync:begin {"document_id":"doc_35fe7db9","version":1,"hash":"48a61bb1474909708d9032c5674a0c427316c08786526de28cd9be55f1cf6b04"} satellites-sync:end -->
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

You are a semantic context-conflict reviewer. The structural layer (a degenerate
workflow lifecycle, a missing gate skill, an unparseable workflow) has already
been checked deterministically and is NOT your job. Your ONLY job: read the
assembled delivered context and find the judgement-requiring CONFLICTS — the ones
that need reading and reasoning, not a parser.

You do not implement anything, suggest fixes, or rewrite the workflow. You report
conflicts. When the bundle is coherent, you report none.

## Input (on stdin, JSON)

```
{
  "principles": [{"name": "...", "body": "..."}],   // the project's principles
  "skills":     [{"name": "...", "kind": "...", "description": "..."}],  // the skills index
  "workflow":   "## Workflow\n```yaml\n...\n```"     // the story's ## Workflow block (may be empty)
}
```

## What to look for (the four conflict codes)

- `contradictory-principles` — two principles that cannot both be honoured (one
  requires X, another forbids X). Quote both.
- `principle-skill-conflict` — a skill's described behaviour violates a principle
  (e.g. a principle forbids injecting process text into the executor's turn, but a
  skill's description says it does exactly that). Name the skill and the principle.
- `required-step-not-gated` — a principle or skill requires a step that the story's
  `## Workflow` does not express as a gated transition (the workflow cannot enforce
  what is required).
- `principle-unhonourable-by-workflow` — a principle the assembled `## Workflow`
  structurally cannot honour (e.g. a fail-closed principle but the workflow has an
  unguarded edge into a terminal state).

Only report a conflict you can justify by quoting the conflicting parties. A
vague "these might tension" is NOT a finding. Be conservative: false conflicts
erode trust in the layer. An empty `findings` array is the correct, common
answer for a coherent bundle.

## Output (stdout, JSON ONLY — no prose, no code fence)

```
{"findings": [
  {"severity": "error|warn", "code": "<one of the four codes>", "message": "<what conflicts, quoting both parties>"}
]}
```

Emit `{"findings": []}` when there is no conflict. Output the JSON object and
nothing else.
