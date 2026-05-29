---
name: ledger-spine
scope: project
workspace_id: wksp_6f048cd8
project_id: proj_fc7d72d8
type: document
tags: [kind:contract]
---

# Ledger spine

The ledger is the absolute record of a story's life — every action the
harness took, in order, so a person can assess what happened without
re-running anything. Raw is fine; illegible is not.

## The spine

Beneath the raw `log:` lines a story carries a small set of structured
entries that read as its life at a glance:

- `run_started` — the client requested work on the story (which agent,
  which run).
- `context_loaded` — the harness delivered load-context and principles
  to the agent.
- `review_requested` — a gated transition asked for a decision.
- `review_accept` / `review_reject` — the verdict and its notes.
- `status_transition` — the state the story moved to.
- `run_finished` — the run ended, with its outcome.

Read top to bottom these answer: who acted, what context they had, what
was reviewed, and where the status went.

## Raw underneath

The subprocess's own output stays in the ledger as `log:info` /
`log:warn` rows, correlated by run id. The spine is the index; the raw
lines are the detail one level down.

## Testing a run

The ledger is also the test harness. To check whether the context,
principles, and process behave as configured, have the satellites
client spawn `claude -p` against a story; the subprocess's spine and
raw rows are the result you read back — no interactive session needed.

Principles load at two scopes — global (system) and project/workspace.
A change to project-scoped context shows up in the next `claude -p` run
at once. A change to global context is cached at session start, so
observing it in an open session needs a Claude restart; the `claude -p`
path sidesteps that by starting a fresh subprocess each run.

## Why it matters

You cannot simplify a path you cannot see. The spine turns "the agent
did something" into "the agent loaded X, planned Y, was rejected on Z"
— the only way to find the waste.

See [[agent-goals]], [[reviewer-process]].
