---
title: Stories
slug: stories
order: 10
tags: [help, stories]
---
# Stories

A **story** is the unit of deliverable work — the finest grain
that survives lifecycle audit. Epics are tags
(`epic:<slug>`), not primitives; sub-stories and subtasks do not
exist (per principle pr_a9ccecfb).

## Lifecycle

A story IS its task chain — the rows where `story_id=<id>`,
ordered by `created_at`. The orchestrator submits the full task
list up front via `story_task_submit(kind=plan, tasks=[…])`.
Each contract surfaces as a paired (kind=work, kind=review) task.

At each work task:

1. The orchestrator (or a designated agent) **claims** via
   `task_claim` (or `task_claim_by_id`).
2. It performs the work bounded by the agent's
   `permission_patterns`.
3. It writes evidence as ledger rows tagged `task_id:<id>`,
   `kind:evidence`.
4. It **closes** via `story_task_submit(kind=close, task_id=<id>,
   outcome=success|failure, evidence_ledger_ids=[…])`. The
   substrate publishes the paired review task automatically.

The autonomous reviewer service then claims the review task, runs
the rubric (the matching agent's body) against the evidence, and
either accepts (success) or rejects (failure). On rejection it
spawns a successor work + paired planned-review pair carrying
`prior_task_id` so the orchestrator can dispatch a fresh attempt.

## Filing stories

- Tied to a document where possible (per principle
  pr_10c48b6c — "documents drive feature stories"). Bug, infra,
  and ops stories may not need a document source.
- Tagged with `epic:<slug>` for rollup.
- Acceptance criteria are testable observations, not
  declarative claims.

## Limitations

- A story's status is irreversible once terminal (`done` or
  `cancelled`). Reopens are new stories with a `supersedes`
  reference.
- AC amendments require the `story_update` ceremony, not silent
  edits.
