---
name: default
required_slots:
  - { contract_name: preplan,       required: true, min_count: 1, max_count: 1 }
  - { contract_name: plan,          required: true, min_count: 1, max_count: 1 }
  - { contract_name: develop,       required: true, min_count: 1, max_count: 10 }
  - { contract_name: push,          required: true, min_count: 1, max_count: 1 }
  - { contract_name: merge_to_main, required: true, min_count: 1, max_count: 1 }
  - { contract_name: story_close,   required: true, min_count: 1, max_count: 1 }
tags: [v4, system]
---
# Default System Workflow

The default 6-slot lifecycle every story passes through unless its
project carries a project-scope workflow override.

## Shape

`preplan → plan → develop → push → merge_to_main → story_close`

- `preplan` (1) — readiness gate.
- `plan` (1) — implementation strategy + review criteria.
- `develop` (1–10) — code edits + tests + commit. Multiple develop
  CIs are permitted when a story splits naturally (e.g. backend
  then frontend), but each is its own CI with its own evidence.
- `push` (1) — ship to origin.
- `merge_to_main` (1) — local sync.
- `story_close` (1) — transition + reviewer verdict.

## How it's used

When `satellites_story_workflow_claim` is called, the server
validates the proposed contract list against this workflow's
required slots. Missing required slots reject the claim; extra
optional slots (when supported by the project's workflow_spec) are
permitted between the required ones.

## Limitations

- This is a **system** workflow. Project-scope overrides live as
  per-project workflow documents and supersede this one when set.
- The slot order is enforced by the server. Stories cannot skip
  preplan or plan, even when the change appears trivial.
- Adding a new contract type to the lifecycle requires both a
  contract markdown file (config/seed/contracts/) and an update
  to this workflow's `required_slots` to include it.
