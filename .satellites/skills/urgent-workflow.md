---
name: urgent-workflow
type: skill
kind: workflow
tags: [kind:workflow]
applies_to: [urgent]
description: The lifecycle an `urgent` story follows — plan → in-progress → deploy → done, every edge reviewer-gated. A lean four-state fast path for work that must ship quickly WITHOUT skipping the gates. Invoke when implementing an `urgent` story; it IS the executor's process.
---

# Urgent workflow

The fast path for an `urgent` story — four states, three gates. Speed comes from removing ceremony, never from skipping review. The story is the goal; this is the loop.

1. `document_get` the story; read its acceptance criteria.
2. Before starting, `document_upsert` two sections into the story body:
   - **`## Workflow`** — the fenced ```yaml block below, copied verbatim. Later gates parse the story's copy.
   - **The plan** — Purpose / Approach / numbered Acceptance criteria.
3. Request the entry gate: `satellites story status_transition <story-id> --skill <gate>`. The plan-review accept IS your go-ahead to start.
4. Do the work; run the tests until they pass; commit at each checkpoint.
5. Request the next gate until `done`: deploy-review confirms the work is complete and tests pass, then ship the change (this repo's ship step), then done-review verifies it shipped.

A rejected gate returns notes; fix and request again. Only reviewers advance status — never hand-patch it.

A client/server change is invisible to the gate until it is built, tested, shipped through this repo's deploy step, and the client refreshed; the gate runs the local binary, so an unshipped change is not seen.

## Transitions

- `backlog → in-progress` — `satellites-urgent-plan-review`: sound plan and a pinned `## Workflow`.
- `in-progress → deploy` — `satellites-urgent-deploy-review`: work complete, tests pass, acceptance criteria met.
- `deploy → done` — `satellites-urgent-done-review`: change deployed and verified.

```yaml
states:
  - backlog
  - in-progress
  - deploy
  - done
transitions:
  - {from: backlog,     to: in-progress, reviewer_skill: "satellites-urgent-plan-review"}
  - {from: in-progress, to: deploy,      reviewer_skill: "satellites-urgent-deploy-review"}
  - {from: deploy,      to: done,        reviewer_skill: "satellites-urgent-done-review"}
```
