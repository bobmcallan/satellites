---
name: task_lifecycle
kind: task
statuses:
  - planned
  - published
  - claimed
  - in_flight
  - closed
  - archived
transitions:
  planned:    [published, closed]
  published:  [claimed, closed]
  claimed:    [in_flight, closed, published]
  in_flight:  [closed]
  closed:     [archived]
  archived:   []
default_status_on_create: planned
subscriber_visible_statuses: [published, claimed, in_flight]
tags: [v4, lifecycle, system]
---
# Task lifecycle

The substrate's task primitive flows through a small set of statuses.
This document is the canonical source of truth — `internal/task` reads
it at boot and rejects illegal transitions per the matrix above.

## States

- **planned** — drafted by an agent. Persisted, but invisible to
  subscribers (the reviewer service, future task_claim workers). The
  agent owns the row; it can edit, reorder, or cancel without
  affecting downstream consumers.
- **published** — committed to the work queue. Subscribers see it on
  their next scan and may claim it.
- **claimed** — a subscriber has taken ownership. Released back to
  `published` if the worker drops it.
- **in_flight** — actively being processed. Optional intermediate
  state for long-running tasks.
- **closed** — terminal, with an `outcome` (success / failure /
  timeout). The retention sweep eventually moves closed rows to
  `archived`.
- **archived** — terminal post-retention. Row stays in storage for
  audit; the default `task_list` query excludes it.

## Transitions

The matrix in frontmatter is authoritative. In prose:

- `planned → published` is the orchestration step (`task_publish`).
- `planned → closed` lets an agent cancel a draft without ever
  publishing it.
- `published → claimed` is the subscriber's `task_claim` taking
  ownership.
- `claimed → published` is the worker dropping the row back on the
  queue (e.g. claim-expiry watchdog).
- `claimed → in_flight` is optional — workers that progress through
  intermediate state mark it; simple workers go straight to closed.
- `closed → archived` is the retention sweep (`sty_dc2998c5`).

## Why a separate `planned` state

An agent's working list (drafting, reordering, batching) is not the
same as the committed queue. Conflating them — as `task_enqueue` did
before sty_c1200f75 — leaks half-formed plans into the subscriber's
view and removes the orchestration opportunity. Splitting the states
gives the orchestrator a place to stand: plan locally, then publish.

## Verbs

- `task_plan(...)` writes a task at `planned`. The agent's draft.
- `task_publish(task_id)` flips a `planned` row to `published`. Or, as
  shorthand, `task_publish` may accept the same args as `task_plan`
  and create+publish in one call (skipping the planned step) for
  callers that don't need the staging.
- `task_enqueue(...)` is preserved as an alias of `task_publish` for
  back-compat. Existing callers keep working; new callers use the
  explicit verbs.

## Subscriber-visible statuses

Subscribers (reviewer service, future task workers) filter by
`subscriber_visible_statuses`: rows in `planned` are never visible.
This is the substrate's invariant — without it, partial agent work
leaks into the workforce.
