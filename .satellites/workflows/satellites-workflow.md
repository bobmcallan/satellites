---
name: satellites-workflow
kind: workflow
tags: [kind:workflow]
applies_to: ["*"]
description: The lifecycle EVERY satellites story follows (any category) — backlog → plan-reviewed → ready → in_progress → techdebt-review → integration-review → done-review → done, reviews as visible states with actors, fail loops bounded in code (×3, exhaustion → blocked). Invoke when implementing a story; it IS the executor's process.
---

# Satellites workflow

The single executable workflow for this repo's stories, whatever their
category. The story is the goal; this is the loop. (Anchors — parent stories
that group children and carry no executable work — follow
`satellites-parent-workflow` instead.) Reviews are STATES with actors: the
status itself answers "whose turn is it, and was the gate run?".

1. `document_get` the story; read its acceptance criteria.
2. Before starting, prepare the story body:
   - **`## Workflow`** — run `satellites workflow embed <story-id>`. It resolves
     this workflow by the story's category (the `applies_to` match over
     `.satellites/workflows/` then the materialised skills), stamps the fenced
     ```yaml block into the story, and prints the workflow + the next gate. Do
     NOT hand-copy the yaml. Later gates parse the embedded copy.
   - **The plan** — `document_upsert` Purpose / Approach / numbered Acceptance criteria.
3. Request the entry gates in order: `satellites story status_transition
   <story-id> --skill <gate>` for the comprehensive plan-review (backlog →
   plan-reviewed), then the intent gate (plan-reviewed → ready) which judges the
   plan against the [[constitution]] — rejecting a story that
   proposes baking a gate/process/opinion into the binary instead of the
   substrate — then start-review (ready → in_progress).
4. Do the work. At every natural checkpoint, request the checkpoint traverse:
   `satellites story status_transition <story-id> --skill
   satellites-technical-debt-review` — the client enacts the checkpoint edge into
   `techdebt-review` (a reviewer state) and stops ("reviewer's turn"). Request the
   same gate AGAIN from `techdebt-review`: it runs its functional check (build +
   unit + the integration tier) against the LOCAL working tree and reconciles the
   failures against the register. A fail returns the story to `in_progress` with
   nothing shipped (the 3rd fail escalates to `blocked`); a pass lands
   `integration-review` and releases the ship.
5. On pass, run the **checkpoint capability** (below) — commit, push, watch
   CI, record evidence — so the verified tree becomes the pushed commit the
   reviewers judge. Nothing ships from a tree whose traverse failed.
6. From `integration-review`, request `satellites story status_transition
   <story-id> --skill satellites-integration-test-review` — it judges the
   UI/DOGFOOD criteria's evidence (named tests in tests/integration/, run
   green by the traverse, tier-conformant); a story with no browser surface
   accepts trivially. Pass lands `done-review`; fail returns to `in_progress`
   (×3, then `blocked`).
7. From `done-review`, request `satellites story status_transition <story-id>
   --skill satellites-story-done-review`. The gates JUDGE ONLY on these edges —
   the client enacts pass → `done` / fail → back to `in_progress` (×3, then
   `blocked`).

A rejected gate returns notes; fix and request again — each reject is a real
transition back to `in_progress`, visible on the ledger. Only reviewers and the
client's deterministic enactment advance status — never hand-patch it. A story
in `blocked` is the operator's: stop, surface it, and do not work around it.
When the ×N exhaustion that landed it there was a recoverable FLAKE (not a
genuine product failure) and you have fixed it, you may request recovery —
`satellites story status_transition <id> --skill satellites-loop-recovery-review`
— which judges a `## Recovery` rationale in the body and, on accept, returns the
story to `in_progress` with a fresh quota (the `exhausted` marker already reset
the reject count). A genuine failure stays blocked for the operator.

A client/server change is invisible to the gate until it is built, tested,
committed, pushed through CI, and the client refreshed; the gate runs the local
binary, so an unshipped change is not seen.

## States and actors

- `plan-reviewed` (reviewer) — the comprehensive plan-review has passed; `satellites-intent-plan-review` judges the plan against the constitution before the story is `ready`.
- `in_progress` (executor) — the work happens here, and every fail edge lands back here.
- `techdebt-review` (reviewer) — `satellites-technical-debt-review` judges: its functional check runs build + unit + the integration tier, then it reconciles the failures against the quarantine register (a registered + owned failure is tolerated); the client enacts its pass/fail edge.
- `integration-review` (reviewer) — `satellites-integration-test-review` judges the UI/DOGFOOD evidence by reading the integration result the `techdebt-review` traverse already produced; it carries no functional check and never re-runs the tier (the suite executes exactly once per loop, in `techdebt-review`). Trivial accept when no browser surface; the client enacts its decision.
- `done-review` (reviewer) — `satellites-story-done-review` judges; the client enacts its decision.
- `blocked` (operator) — fail-loop exhaustion lands here; the operator moves a story out by one of two operator-owned edges: `satellites-loop-recovery-review` (blocked → in_progress) when the exhaustion was a recoverable, fixed flake, or `satellites-story-cancel-review` (blocked → cancelled) to terminally retire a genuinely-failed story. A genuine failure that is not being retired stays the operator's.

## Checkpoint gates

This definition names every fail-closed check that runs while driving a story —
nothing gate-like runs outside it. The checkpoint order at every natural
checkpoint (end of phase, meaningful change, before requesting review) is
**verdict, then ship**:

- [[satellites-technical-debt-review]] — the `techdebt-review` STATE's reviewer
  gate (step 4): its functional `check:` runs build / unit / the integration tier
  against the local tree and it reconciles the failures against the register
  BEFORE anything ships. Its verdict lands as a ledger row; a fail leaves the
  remote untouched. The gate owns the decision rule — config, not a binary command.
  This is the SINGLE point the test tiers execute in the local loop — the
  downstream `integration-review` reads this result and never re-runs (CI re-runs
  the tiers as the non-skippable backstop, not in-loop duplication). The gate is
  KEPT as distinct from `integration-review`: it judges broken-windows debt
  (every failing check is owned in the register), a concern orthogonal to
  integration-review's UI/DOGFOOD coverage judgement — it is a genuine
  debt-register review, not a re-skinned test runner.

A traverse pass releases the [[satellites-commit-push]] capability (step 5),
which executes the remaining atomic gates pre-commit and honours their verdicts
(each gate owns its decision rule):

- [[satellites-doc-drift-review]] — when the change touches the CLI.
- [[satellites-global-button-style-review]] — when the change touches the portal UI.
- [[satellites-workflow-drift-review]] — when the change touches process configuration (skills, principles, workflows).
- [[satellites-agent-architecture-review]] — when the change touches the agent/executor surface (internal/agent, the agent executor in internal/verb, agent operating documents); a judgment gate critiquing the change for configuration-over-code.
- [[satellites-intent-code-review]] — the general config-over-code gate, judged on the diff against the constitution: rejects a gate, workflow, check, process step, or opinion baked into the binary where the substrate already holds its kind. The agent-architecture-review is its narrow agent-surface case.

```yaml
states:
  - backlog
  - {name: plan-reviewed,      actor: reviewer}
  - ready
  - {name: in_progress,        actor: executor}
  - {name: techdebt-review,    actor: reviewer}
  - {name: integration-review, actor: reviewer}
  - {name: done-review,        actor: reviewer}
  - {name: blocked,            actor: operator}
  - done
  - cancelled
transitions:
  - {from: backlog,            to: plan-reviewed,   reviewer_skill: "satellites-story-plan-review"}
  - {from: plan-reviewed,      to: ready,           reviewer_skill: "satellites-intent-plan-review"}
  - {from: ready,              to: in_progress,     reviewer_skill: "satellites-story-start-review"}
  - {from: in_progress,        to: techdebt-review, trigger: checkpoint}
  - {from: techdebt-review,    on: pass, to: integration-review, reviewer_skill: "satellites-technical-debt-review"}
  - {from: techdebt-review,    on: fail, to: in_progress, max_iterations: 3, on_exhausted: blocked, reviewer_skill: "satellites-technical-debt-review"}
  - {from: integration-review, on: pass, to: done-review, reviewer_skill: "satellites-integration-test-review"}
  - {from: integration-review, on: fail, to: in_progress, max_iterations: 3, on_exhausted: blocked, reviewer_skill: "satellites-integration-test-review"}
  - {from: done-review,        on: pass, to: done, reviewer_skill: "satellites-story-done-review"}
  - {from: done-review,        on: fail, to: in_progress, max_iterations: 3, on_exhausted: blocked, reviewer_skill: "satellites-story-done-review"}
  - {from: blocked,            to: in_progress,     reviewer_skill: "satellites-loop-recovery-review"}
  - {from: backlog,            to: cancelled,       reviewer_skill: "satellites-story-cancel-review"}
  - {from: plan-reviewed,      to: cancelled,       reviewer_skill: "satellites-story-cancel-review"}
  - {from: ready,              to: cancelled,       reviewer_skill: "satellites-story-cancel-review"}
  - {from: in_progress,        to: cancelled,       reviewer_skill: "satellites-story-cancel-review"}
  - {from: blocked,            to: cancelled,       reviewer_skill: "satellites-story-cancel-review"}
```

## Environment

Drives story documents and the repo working tree: upserts the story body,
requests gated transitions, and triggers checkpoint commits.

```yaml
guardrails:
  always:
    - Copy the Workflow yaml block into the story verbatim before requesting a gate.
    - Route every status change through the transition's reviewer skill or the client's deterministic enactment.
    - Run the techdebt-review traverse to a pass before the checkpoint capability ships anything.
  ask_first:
    - Cancelling a story (the rationale is the operator's call unless already given).
  never:
    - Hand-patch story status or bypass a gate verdict.
    - Act in a state whose actor is not yours — blocked is the operator's.
    - Run a fail-closed check this definition does not name.
```
