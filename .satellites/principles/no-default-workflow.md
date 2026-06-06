---
name: no-default-workflow
type: document
tags: [principles:project, area:workflow, area:reviewer]
---

# No default workflow — design the lifecycle from the requirement

A story's `## Workflow` is **designed from that story's requirement**, not copied
unread from the category default. The category default (`satellites-fix-workflow`,
`satellites-feature-workflow`, …) is a **starting point**, not the answer.

## Why

The defaults are deliberately minimal: `backlog → in_progress → done`, gated by
plan-review and done-review. But a story's real requirements often need MORE than
that. The enforcement contract (`enforcement-contract`, rule 2) is explicit:

> Anything the user requires — review gates, a commit-push / ship checkpoint, a
> techdebt / integration gate — **must be a gated transition in the definition**,
> not prose or a "trusted capability". An un-gated step is a skippable step.

The semantic context-conflict reviewer (`satellites context review --semantic`)
proves the gap live: against the bare fix-workflow it flags
`required-step-not-gated` for the commit-push checkpoint
(`commit-push-after-each-story`) and the techdebt gate (`broken-windows`) — both
required, neither gated by the default. Copying the default unchanged ships a
workflow that **cannot enforce what the story requires**.

## The rule

- **Design the workflow.** Use `satellites workflow design <story>` (the isolated
  design agent) to author a `## Workflow` from the requirement, or hand-author one
  — then justify it in the story body. The workflow should gate every step the
  story's requirements + the applicable principles demand.
- **The default is allowed when it genuinely fits** — a story whose only required
  gates are plan + done review may keep the default. But that is a *decision*, not
  a reflex; the body should say so.
- **plan-review accepts any sound, valid workflow** — default or designed —
  provided it passes lifecycle validation and names only materialised gate skills.
  It no longer demands the canonical block verbatim. A verbatim default copied with
  no design rationale is **flagged** (advisory) so the undesigned default is
  visible.

## Relationship

This is the policy `satellites-story-plan-review` enforces and
`satellites-workflow-design` (order:5) serves. It composes with the
context-conflict review (`satellites context review`), whose `required-step-not-gated`
findings are the evidence a default is insufficient for a given story.
