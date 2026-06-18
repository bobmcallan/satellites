---
name: satellites-workflow
kind: workflow
tags: [kind:workflow]
applies_to: ["*"]
description: The lifecycle EVERY satellites story follows (any category) — backlog → plan-reviewed → ready → in_progress → techdebt-review → integration-review → shipping → done-review → done, reviews as visible states with actors, fail loops bounded in code (×3, exhaustion → blocked). Invoke when implementing a story; it IS the executor's process.
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
4. **Do the work in `in_progress`, committing INCREMENTALLY and LOCALLY** — small
   conventional commits as the work progresses, with **no `.version` bump and no
   push** (the push happens once, at `shipping`). At a natural checkpoint, request
   the checkpoint traverse: `satellites story status_transition <story-id> --skill
   satellites-technical-debt-review` — the client enacts the checkpoint edge into
   `techdebt-review` (a reviewer state) and stops ("reviewer's turn"). Request the
   same gate AGAIN from `techdebt-review`: it is a senior-developer **code-debt
   scan** of the diff — duplicated / redundant / dead functions — and runs NO
   tests. A fail returns the story to `in_progress` (the 3rd fail escalates to
   `blocked`); a pass lands `integration-review`.
5. From `integration-review`, request `satellites story status_transition
   <story-id> --skill satellites-integration-test-review` — it carries the
   **broken-windows** policy: its functional check runs build + unit + the
   integration tier against the LOCAL tree, reconciles the failures against the
   register, AND judges the UI/DOGFOOD criteria's coverage (named tests in
   tests/integration/, green in THIS check, tier-conformant). This is the SINGLE
   place the test tiers execute in the local loop. Fail returns to `in_progress`
   (×3, then `blocked`); pass lands `shipping`.
6. In `shipping` (your turn as executor), run the **checkpoint capability**
   [[satellites-commit-push]] — the remaining atomic gates, ONE `.version` bump,
   the final commit + push (folding the incremental commits), watch CI, record
   evidence. Then advance the ungated `shipping → done-review` edge: `satellites
   story set-status <story-id> done-review`. Nothing ships before BOTH reviews
   passed; the integration tier ran locally before the push, and done-review
   judges the pushed commit.
7. From `done-review`, request `satellites story status_transition <story-id>
   --skill satellites-story-done-review`. The gates JUDGE ONLY on these edges —
   the client enacts pass → `done` / fail → back to `in_progress` (×3, then
   `blocked`).

A rejected gate returns notes; fix and request again — each reject is a real
transition back to `in_progress`, visible on the ledger. Only reviewers and the
client's deterministic enactment advance status — never hand-patch it (the lone
exception is the ungated `shipping → done-review` edge, advanced with
`set-status` after commit-push). A story in `blocked` is the operator's: stop,
surface it, and do not work around it. When the ×N exhaustion that landed it
there was a recoverable FLAKE (not a genuine product failure) and you have fixed
it, you may request recovery — `satellites story status_transition <id> --skill
satellites-loop-recovery-review` — which judges a `## Recovery` rationale in the
body and, on accept, returns the story to `in_progress` with a fresh quota. A
genuine failure stays blocked for the operator.

A client/server change is invisible to the gate until it is built, tested,
committed, pushed through CI, and the client refreshed; the gate runs the local
binary, so an unshipped change is not seen.

## States and actors

- `plan-reviewed` (reviewer) — the comprehensive plan-review has passed; `satellites-intent-plan-review` judges the plan against the constitution before the story is `ready`.
- `in_progress` (executor) — the work happens here, with incremental LOCAL commits (no version bump, no push); every fail edge lands back here.
- `techdebt-review` (reviewer) — `satellites-technical-debt-review` is a senior-developer code-debt scan of the diff (duplicated / redundant / dead functions); it runs NO tests; the client enacts its pass/fail edge.
- `integration-review` (reviewer) — `satellites-integration-test-review` carries broken-windows: its functional check runs build + unit + the integration tier, reconciles the failures against the quarantine register (a registered + owned failure is tolerated), and judges the UI/DOGFOOD coverage. The suite executes exactly once per loop, here; the client enacts its pass/fail edge.
- `shipping` (executor) — runs [[satellites-commit-push]]: the remaining atomic gates, ONE `.version` bump, the final commit + push, watch CI, record evidence. Then advance the ungated `shipping → done-review` edge with `set-status`. Nothing ships before techdebt-review and integration-review have passed.
- `done-review` (reviewer) — `satellites-story-done-review` judges the pushed commit against the acceptance criteria; the client enacts its decision.
- `blocked` (operator) — fail-loop exhaustion lands here; the operator moves a story out by one of two operator-owned edges: `satellites-loop-recovery-review` (blocked → in_progress) when the exhaustion was a recoverable, fixed flake, or `satellites-story-cancel-review` (blocked → cancelled) to terminally retire a genuinely-failed story. A genuine failure that is not being retired stays the operator's.

## Checkpoint gates

This definition names every fail-closed check that runs while driving a story —
nothing gate-like runs outside it.

- [[satellites-technical-debt-review]] — the `techdebt-review` STATE's reviewer
  gate (step 4): a senior-developer code-debt scan of the diff — duplicated,
  redundant, or dead functions the change introduces. It runs NO tests and owns
  its decision rule (config, not a binary command).
- [[satellites-integration-test-review]] — the `integration-review` STATE's
  reviewer gate (step 5): the broken-windows policy. Its functional `check:` runs
  build / unit / the integration tier against the local tree exactly once per
  loop, reconciles the failures against the register, and judges UI/DOGFOOD
  coverage. CI re-runs the tiers as the non-skippable backstop, not in-loop
  duplication.

The `shipping` state runs the [[satellites-commit-push]] capability (step 6),
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
  - {name: shipping,           actor: executor}
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
  - {from: integration-review, on: pass, to: shipping, reviewer_skill: "satellites-integration-test-review"}
  - {from: integration-review, on: fail, to: in_progress, max_iterations: 3, on_exhausted: blocked, reviewer_skill: "satellites-integration-test-review"}
  - {from: shipping,           to: done-review}
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
makes incremental in_progress commits, requests gated transitions, and runs the
commit-push capability at the `shipping` state.

```yaml
guardrails:
  always:
    - Copy the Workflow yaml block into the story verbatim before requesting a gate.
    - Route every status change through the transition's reviewer skill or the client's deterministic enactment (the lone set-status exception is the ungated shipping → done-review edge after commit-push).
    - Pass techdebt-review and integration-review before the shipping state ships anything.
  ask_first:
    - Cancelling a story (the rationale is the operator's call unless already given).
  never:
    - Hand-patch story status or bypass a gate verdict.
    - Act in a state whose actor is not yours — blocked is the operator's.
    - Run a fail-closed check this definition does not name.
```
