---
name: satellites-trunk-workflow
type: skill
kind: workflow
applies_to: [feature, fix, chore, infrastructure]
tags: [kind:workflow]
description: Default trunk lifecycle a story follows — backlog → in_progress → done, every edge reviewer-gated. Scaffolded by `satellites init` as a starting point; edit the states, transitions, and gates to fit your process. Invoke when implementing a story of an applies_to category.
---

# Trunk workflow

The default process a story follows in this repo. The story is the goal; this is
the loop. This file is a SCAFFOLD `satellites init` wrote so the repo is not stuck
in engage-only mode — edit the states, transitions, and gate skills below to fit
your real process (per `no-default-workflow`, the workflow is yours to design,
not a binary default).

1. `document_get` the story; read its acceptance criteria.
2. Before starting, embed two sections into the story body via `document_upsert`:
   - **`## Workflow`** — the fenced yaml block below, copied verbatim. Later gates
     parse the story's own copy.
   - **The plan** — Purpose / Approach / numbered Acceptance criteria.
3. Request each gated transition: `satellites story status_transition <story-id> --skill <gate>`.
4. Do the work; commit at each checkpoint.
5. Request the next gate until `done`.

A rejected gate (or a missing/mismatched `## Workflow`) returns notes; fix and
request again. Only reviewers advance status — never hand-patch it.

## Transitions

- `backlog → in_progress` — `satellites-trunk-plan-review` accepts the plan.
- `in_progress → done` — `satellites-trunk-done-review` verifies against the acceptance criteria.

```yaml
states:
  - backlog
  - in_progress
  - done
transitions:
  - {from: backlog,     to: in_progress, reviewer_skill: "satellites-trunk-plan-review"}
  - {from: in_progress, to: done,        reviewer_skill: "satellites-trunk-done-review"}
```
