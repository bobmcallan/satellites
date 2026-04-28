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

Each story flows through a workflow's contract instances (CIs).
At each step:

1. The agent **claims** the contract via `contract_claim`.
2. It performs the work permitted by the contract's
   `permitted_actions`.
3. It **closes** the contract with structured evidence on the
   ledger.
4. The reviewer (LLM or mechanical) accepts or returns
   `needs_evidence`.

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
- AC amendments require the
  `satellites_story_acceptance_criteria_amend` ceremony, not
  silent edits.
