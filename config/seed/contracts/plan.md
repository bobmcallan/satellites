---
name: plan
category: plan
required_categories: [plan]
validation_mode: llm
evidence_required: |
  Two ledger artifacts recorded on the story + plan CI:
  - plan.md  (scope, files-to-change, approach, test-strategy, AC mapping)
  - review-criteria.md  (per-AC verify / evidence / pass-fail boundary)

  Plus at least one downstream task enqueued against the plan CI.
  Each task is bound to its parent CI's stamped agent_id
  (sty_e8d49554) — the agent stamp IS the capability binding under
  the post-sty_92218a87 substrate. A child task carrying an explicit
  agent_id in its payload (matching the downstream CI's stamped
  agent) is the clearest signal for the reviewer; the substrate also
  resolves the agent through the parent CI when the payload is
  silent.
tags: [v4, lifecycle, system]
---
# Plan Contract

Designs the implementation strategy and decomposes the story into
role-tagged child tasks. Plan is the front-floor of every story — the
orchestrator role owns process definition here, including the
readiness assessment.

## What it does

- **Readiness assessment** — relevance, dependencies, prior delivery.
  The plan agent confirms the story is required, blockers are met,
  and the work has not already shipped under a sibling story before
  designing the implementation.
- `plan.md` — the implementation strategy: scope, files-to-change,
  approach, test strategy, AC mapping.
- `review-criteria.md` — the per-AC success conditions, written
  before the implementing agent begins so the criteria are
  independent of the implementing agent's choices.
- **Downstream tasks** — the plan agent enqueues the work the
  downstream contracts (develop, push, merge_to_main, story_close)
  consume. Each task is bound to the plan CI so the story view
  groups work per CI, and inherits its capability scope from the
  per-CI agent stamped at compose time (sty_e8d49554). Plan agents
  may include the downstream CI's `agent_id` in the task payload
  for clarity, but the substrate resolves the binding through the
  parent CI when the payload is silent.

## How

Read-only investigation plus ledger writes plus task enqueues. The
plan agent inspects the codebase and reasons about the change shape;
it never edits a file or runs a build.

## Limitations

- Plan binds develop. Mid-flight scope changes require re-claiming
  the plan CI to amend `plan.md`.
- Plan cannot file follow-up stories during planning — that belongs
  to the user's decision space. Plan can _propose_ splits in
  `plan.md` but does not act on them.
- Plan close requires at least one child task to be enqueued against
  the CI; a plan that designs no work is not a plan.
