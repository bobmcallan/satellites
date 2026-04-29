---
title: Contracts
slug: contracts
order: 30
tags: [help, contracts]
---
# Contracts

A **contract** is one phase in a story's lifecycle. Every contract
defines:

- which **agent role** is allowed to claim it,
- the **permitted tool-call patterns** during the claim window,
- the **evidence shape** the agent must record on close,
- the **validation mode** (LLM reviewer, mechanical checks, or both).

## System contracts

The default lifecycle ships six system contracts:

| Contract | Phase | Writes? |
|---|---|---|
| `preplan` | readiness gate | ledger only |
| `plan` | design | ledger only |
| `develop` | implementation | code + git commit |
| `push` | ship to origin | git push |
| `merge_to_main` | local sync | git merge --ff-only |
| `story_close` | terminal transition | story status flip |

## Configuration

Each contract's markdown lives at
`config/seed/contracts/<name>.md`. Frontmatter carries the
structured payload (`permitted_actions`, `evidence_required`,
`validation_mode`); body is the human description.

## Limitations

- Contract order is enforced server-side. A story cannot skip
  preplan, even when the change appears trivial.
- Contracts cannot be redefined per-story. If a story needs a
  different workflow shape, the orchestrator agent passes
  `proposed_contracts` to `satellites_story_workflow_claim` to override
  the project default.
