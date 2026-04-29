---
name: plan
category: plan
required_role: role_orchestrator
required_categories: [plan]
validation_mode: llm
evidence_required: |
  Two ledger artifacts recorded on the story + plan CI:
  - plan.md  (scope, files-to-change, approach, test-strategy, AC mapping)
  - review-criteria.md  (per-AC verify / evidence / pass-fail boundary)
tags: [v4, lifecycle, system]
---
# Plan Contract

Designs the implementation strategy. Plan runs after preplan passes
and before develop claims, and produces two ledger artefacts that
develop will consume.

## What it does

- `plan.md` — the implementation strategy: scope, files-to-change,
  approach, test strategy, AC mapping.
- `review-criteria.md` — the per-AC success conditions, written
  before the implementing agent begins so the criteria are
  independent of the implementing agent's choices.

## How

Read-only investigation plus ledger writes. The plan agent inspects
the codebase and reasons about the change shape; it never edits a
file or runs a build.

## Limitations

- Plan binds develop. Mid-flight scope changes require re-claiming
  the plan CI to amend `plan.md`.
- Plan does NOT bypass preplan's recommendation. If preplan said
  `block`, plan does not run.
- Plan cannot file follow-up stories during planning — that belongs
  to the user's decision space. Plan can _propose_ splits in
  `plan.md` but does not act on them.
