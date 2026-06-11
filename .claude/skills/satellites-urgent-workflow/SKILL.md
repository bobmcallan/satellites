---
name: urgent-workflow
type: skill
kind: workflow
tags: [kind:workflow]
applies_to: [urgent]
description: The lifecycle an `urgent` story follows — backlog → in-progress → deploy → done, every edge reviewer-gated. A lean four-state fast path for work that must ship quickly WITHOUT skipping the gates. Invoke when implementing an `urgent` story; it IS the executor's process.
---
<!-- satellites-sync:begin {"document_id":"doc_68c91c5d","version":5,"hash":"cce195c0616053c1a3d7e3ce2aeea373ec30eaf66cb43dacd732b0ef0596a54c"} satellites-sync:end -->

# Urgent workflow

The fast path for an `urgent` story — four states, three gates. Speed comes from removing ceremony, never from skipping review. The story is the goal; this is the loop.

1. `document_get` the story; read its acceptance criteria.
2. Before starting, `document_upsert` two sections into the story body:
   - **`## Workflow`** — the fenced ```yaml block below, copied verbatim. Later gates parse the story's copy.
   - **The plan** — Purpose / Approach / numbered Acceptance criteria.
3. Request the entry gate: `satellites story status_transition <story-id> --skill <gate>`. The plan-review accept IS your go-ahead to start.
4. Do the work; run the tests until they pass; commit at each checkpoint.
5. Request the next gate until `done`: deploy-review confirms the work is complete and tests pass, then ship the change via the project's deploy procedure (`<deploy-skill>` — the deploy/ship skill named in the story's `## Workflow` or the repo's deploy skill; if none is defined, ask the operator), then done-review verifies it shipped.

A rejected gate returns notes; fix and request again. Only reviewers advance status — never hand-patch it.

A client/server change is invisible to the gate until it is built, tested, and shipped through `<deploy-skill>`, then the client refreshed; the gate runs the local binary, so an unshipped change is not seen.

## Environment

This skill acts on both the story (status mutations via reviewer-gated transitions) and the repo (commits, the deploy/ship step). It does not advance status directly — every state change is a reviewer gate.

```yaml
guardrails:
  always:
    - Pin the verbatim `## Workflow` block into the story before requesting the entry gate.
    - Run the tests to green before requesting deploy-review.
    - Let reviewers advance status; request gates, never write status directly.
  ask_first:
    - Running the deploy/ship step (`<deploy-skill>`) when the deploy procedure is undefined or ambiguous.
  never:
    - Skip or bypass a gate (e.g. `--skip-review`) to move a story faster.
    - Hand-patch story status outside a reviewer transition.
```

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
