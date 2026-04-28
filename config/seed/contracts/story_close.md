---
name: story_close
category: story-close
required_role: role_orchestrator
required_categories: [story-close]
validation_mode: llm
permitted_actions:
  - "Read:**"
  - "mcp__satellites__satellites_*"
evidence_required: |
  Story review result recorded on the ledger with
  {review_status, score, story_id}.
tags: [v4, lifecycle, system]
---
# Story Close Contract

Transitions a story to its terminal state once all delivery
contracts have passed. Story close is the lifecycle gate that flips
the story to `done` (or `cancelled`) and records the closing
reviewer verdict.

## What it does

- Calls `satellites_story_close` with one of:
  `delivered`, `plan_only`, `not_required`, `duplicate`,
  `superseded`, `failed:complexity`, `failed:scope_invalid`,
  `failed:blocked`.
- Records a `kind:closing-review` ledger row capturing
  review_status + score + story_id.

## How

Read-only across the codebase, MCP read + write to the close verb
and ledger.

## Limitations

- Cannot bypass the close gate. `needs_evidence` from the LLM
  reviewer means the agent surfaces the gap and stops — it does
  not invent evidence.
- Cannot retroactively edit prior CIs to make the close pass.
- One terminal transition per story; once `done` or `cancelled`,
  the story is immutable.
