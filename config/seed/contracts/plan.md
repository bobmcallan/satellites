---
name: plan
category: plan
required_categories: [plan]
validation_mode: llm
evidence_required: |
  Two ledger artifacts recorded on the story + plan CI:
  - plan.md  (scope, files-to-change, approach, test-strategy, AC mapping)
  - review-criteria.md  (per-AC verify / evidence / pass-fail boundary)

  Plus at least one child task enqueued against the plan CI, tagged
  with the role required to execute it (e.g. required_role:developer).
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
- **Child tasks** — the plan agent enqueues the work the downstream
  contracts (develop, push, merge_to_main, story_close) consume.
  Each task carries the role required to execute it
  (`required_role:developer`, `required_role:reviewer`,
  `required_role:releaser`, etc.) and is bound to the plan CI so the
  story view groups work per CI.

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
