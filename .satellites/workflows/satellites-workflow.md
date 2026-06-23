---
name: satellites-workflow
kind: workflow
tags: [kind:workflow]
applies_to: ["infrastructure", "bug", "fix", "feature", "improvement", "skill", "chore", "documentation"]
description: The reviewers-only lifecycle for this repo's executable-work stories (infrastructure, bug, fix, feature, improvement, skill, chore, documentation) — backlog to in_progress to integration to shipping to summary to done. Four reviewers gate it (the only enforcement) — satellites-intent-plan-review opens the story; satellites-integration-review runs the integration tier at the `integration` state (after in_progress, before commit-push) and advances integration to shipping on a green tier (fail returns to in_progress); satellites-commit-push-review judges that the executor's commit-push step landed and advances shipping to summary (a fail returns to in_progress to repair and re-ship); and satellites-implementation-summary-review judges that the story records what it changed and why and advances summary to done (a fail returns to in_progress). The executor reaches the goal any way it can; only a reviewer's accept moves the status.
---
<!-- satellites-sync:begin {"document_id":"doc_e99ba77f","version":6,"hash":"25cefc9c6d45b980b1e70b5533d68612bce5f0b881a064d48db13dfe2efb507f"} satellites-sync:end -->
# Satellites workflow (reviewers-only)

The single enforcement primitive is the reviewer (see [[reviewer-only-model]]): a
`claude -p` judgment that enacts a transition and that the agent cannot bypass.
Every gated edge names a reviewer that exists, so it always resolves.

The lifecycle is `backlog → in_progress → integration → shipping → summary → done`,
four reviewers gating it: `satellites-intent-plan-review` opens the story; the executor
does the work and, at a natural checkpoint, advances the ungated `in_progress →
integration` checkpoint edge (`--checkpoint`, never a gate side-effect).
At `integration` the executor requests `satellites-integration-review`, which runs
the `-tags integration` tier (testcontainers + chromedp) as its functional check and
enacts **pass → shipping** or **fail → in_progress** (fix the red tier and re-request,
bounded) — so a broken integration tier blocks the ship locally, before commit-push,
not only in CI. At `shipping` the executor performs the **commit-push step** (final
commit, `.version` bump when a binary path changed, push, watch the test → release →
deploy CI chain), then requests `satellites-commit-push-review`, which enacts
**pass → summary** or **fail → in_progress** (repair and re-ship, bounded). At
`summary` — the code has shipped, HEAD is the story's final commit — the executor
records a `## Implementation summary` in the story body (the files changed, what
changed and why) and requests `satellites-implementation-summary-review`, which enacts
**pass → done** or **fail → in_progress**, so every closed story leaves a readable
record of its change. This is the code-diff summary, distinct from the ledger-narrative
`satellites-story-summary` the summary hook records.

**Abandon.** A story still in `backlog` or `in_progress` may be retired to the
terminal `cancelled` state via `satellites-story-cancel-review`, which accepts the
move only on a concrete `## Cancellation` rationale (superseded by a named
artifact, or not-required with a reason). Cancellation is orthogonal to the
forward lifecycle.

**Shared step.** The commit-push edge is the reusable `{work_skill, reviewer}`
atom — `work_skill: commit-push` + `reviewer_skill: satellites-commit-push-review`
— the SAME step a task workflow can name (see `document:project/workflow-schema`,
whose canonical step example is exactly this pair). One atom at every scale: a
standalone task is a one-step repeatable workflow; this story-workflow step is
that same task-shaped atom. The reviewer is the sole status-updater; the
`commit-push` work skill is the "do" half it judges.

## Workflow

```yaml
states:
  - backlog
  - {name: in_progress, actor: executor}
  - {name: integration, actor: executor}
  - {name: shipping, actor: executor}
  - {name: summary, actor: executor}
  - {name: blocked, actor: operator}
  - done
  - cancelled
transitions:
  - {from: backlog, to: in_progress, reviewer_skill: "satellites-intent-plan-review"}
  - {from: in_progress, to: integration, trigger: checkpoint}
  - {from: integration, to: shipping, reviewer_skill: "satellites-integration-review", on: pass}
  - {from: integration, to: in_progress, reviewer_skill: "satellites-integration-review", on: fail, max_iterations: 3, on_exhausted: blocked}
  - {from: shipping, to: summary, work_skill: "commit-push", reviewer_skill: "satellites-commit-push-review", on: pass}
  - {from: shipping, to: in_progress, work_skill: "commit-push", reviewer_skill: "satellites-commit-push-review", on: fail, max_iterations: 3, on_exhausted: blocked}
  - {from: summary, to: done, reviewer_skill: "satellites-implementation-summary-review", on: pass}
  - {from: summary, to: in_progress, reviewer_skill: "satellites-implementation-summary-review", on: fail, max_iterations: 3, on_exhausted: blocked}
  - {from: backlog, to: cancelled, reviewer_skill: "satellites-story-cancel-review"}
  - {from: in_progress, to: cancelled, reviewer_skill: "satellites-story-cancel-review"}
```
