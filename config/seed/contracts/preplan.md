---
name: preplan
category: pre-plan
required_role: role_orchestrator
required_categories: [preplan]
validation_mode: llm
permitted_actions:
  - "Read:**"
  - "Grep:**"
  - "Glob:**"
  - "Bash:git_log"
  - "Bash:git_status"
  - "mcp__satellites__satellites_*"
evidence_required: |
  Structured preplan assessment recorded on the ledger with
  {relevance, ac_assessment, dependencies, prior_delivery,
  recommendation, pipeline_selected, pipeline_reasoning,
  ac_amendments?}.
tags: [v4, lifecycle, system]
---
# Preplan Contract

Validates that a story is ready for plan and develop before any
implementation effort begins. Preplan is the cheap gate that catches
stale, duplicated, or out-of-scope stories before they consume a
plan + develop cycle.

## What it does

The preplan agent answers four questions:

- **Relevance** — required, obsolete, or duplicate?
- **Dependencies** — are blockers met?
- **Prior delivery** — has the work already shipped under a sibling
  story?
- **Recommendation** — proceed, improve_acs, cancel, or block?

The answer is recorded as a structured ledger row plus an optional
list of AC amendments. The reviewer accepts or rejects on that
evidence.

## How

Read-only investigation. The contract does not permit file edits,
build/test runs, or git mutations.

## Limitations

- Preplan does NOT design the implementation. It only judges
  readiness. The plan contract owns scope + files-to-change.
- Preplan does NOT amend ACs unilaterally. Amendments are proposals
  that the user (or the AC-amendment ceremony) ratifies before
  applying via `satellites_story_acceptance_criteria_amend`.
- Preplan can recommend `cancel` but cannot close the story —
  the story_close contract owns the transition.
