---
name: satellites-feature-workflow
type: skill
kind: workflow
tags: [kind:workflow]
applies_to: [feature]
description: The lifecycle a `feature` story follows — backlog → ready → in_progress → done, every edge reviewer-gated. Invoke when implementing a feature story; it IS the executor's process.
---
<!-- satellites-sync:begin {"document_id":"doc_68ab47dc","version":5,"hash":"3877c71027eacfe8a27188f68ad372d8266fd8810102d57d08240971c2b2845e"} satellites-sync:end -->

# Feature workflow

The process for a `feature` story. The story is the goal; this is the loop.

1. `document_get` the story; read its acceptance criteria.
2. Before starting, `document_upsert` two sections into the story body:
   - **`## Workflow`** — the fenced ```yaml block below, copied verbatim. Later gates parse the story's copy.
   - **The plan** — Purpose / Approach / numbered Acceptance criteria.
3. Request each gated transition: `satellites story status_transition <story-id> --skill <gate>`. The plan-review accept IS your go-ahead to start.
4. Do the work; commit at each checkpoint.
5. Request the next gate until `done`.

A rejected gate (or a missing/mismatched `## Workflow`) returns notes; fix and request again. Only reviewers advance status — never hand-patch it.

A client/server change is invisible to the gate until it is built, tested, committed, pushed through CI (test → release → deploy), and the client refreshed; the gate runs the local binary, so an unshipped change is not seen.

## Transitions

- `backlog → ready` — `satellites-story-plan-review` accepts the plan.
- `ready → in_progress` — `satellites-story-start-review` confirms ready to start.
- `in_progress → done` — `satellites-story-done-review` verifies against the acceptance criteria.

```yaml
states:
  - backlog
  - ready
  - in_progress
  - done
transitions:
  - {from: backlog,     to: ready,       reviewer_skill: "satellites-story-plan-review"}
  - {from: ready,       to: in_progress, reviewer_skill: "satellites-story-start-review"}
  - {from: in_progress, to: done,        reviewer_skill: "satellites-story-done-review"}
```
