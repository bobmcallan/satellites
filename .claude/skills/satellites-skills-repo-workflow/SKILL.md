---
name: satellites-skills-repo-workflow
kind: workflow
tags: [kind:workflow]
applies_to: ["skill"]
description: The lifecycle a skills-repo story follows — author → review → publish. A kind:workflow composed by reference of the existing authoring/review capabilities, selected by applies_to for skills-repo (category "skill") stories. Distinct from the product satellites-workflow; invoke when driving a story that authors and publishes a skill or principle.
---
<!-- satellites-sync:begin {"document_id":"doc_81248e06","version":1,"hash":"dc00398ff0be78b5d03d17c46323cbe735d5fa9f696922b114281a64bc6e72d6","publisher":"proj_682cfeed"} satellites-sync:end -->
<!-- satellites-library:begin {"publisher":"proj_682cfeed","repo":"git@github.com:bobmcallan/satellites-skills.git","commit":"9bab7865f152116a142d8f8b256b8e73224ca2c7"} satellites-library:end -->
# Skills-repo workflow

The governing workflow for a **skills repo** — a `type:skills` publisher whose
unit of work is a skill or principle, not product code. A skills-repo story
moves author → review → publish; this is the loop. It is a sibling of the
product `satellites-workflow`, selected by `applies_to` ↔ story category
(`skill`), never by a filename. Reviews are STATES with actors: the status
answers "whose turn is it, and was the gate run?".

It **composes the existing authoring/review capabilities by reference** — it
duplicates none of their logic. The author and review steps are
`[[satellites-skill-authoring]]` / `[[satellites-principle-authoring]]` and
`[[satellites-skill-review]]` / `[[satellites-principle-review]]`; the publish
step is `satellites skill publish` (a skill) or `satellites principle upload`
(a principle). Each capability is named, never restated.

1. `document_get` the story; read its acceptance criteria.
2. Before starting, `document_upsert` two sections into the story body:
   - **`## Workflow`** — the fenced yaml block below, copied verbatim. Later gates parse the story's copy.
   - **The plan** — Purpose / Approach / numbered Acceptance criteria.
3. Request the entry gates: `satellites story status_transition <story-id> --skill <gate>`
   for plan-review (backlog → ready) and start-review (ready → in_progress).
4. **Author → review → publish** in `in_progress`:
   - **AUTHOR** the skill/principle into `.satellites/{skills,principles}/`,
     following `[[satellites-skill-authoring]]` / `[[satellites-principle-authoring]]`
     (the Spec / Verifier / Environment shape).
   - **REVIEW** it with `[[satellites-skill-review]]` / `[[satellites-principle-review]]`
     and address the critique; the upload content gate is the fail-closed half.
   - **PUBLISH**: `satellites skill publish <name>` promotes a skill to the
     library under this repo's publisher namespace; `satellites principle upload`
     publishes a principle. The auto-publish CI step does the same on merge.
5. At every natural checkpoint request the traverse FIRST:
   `satellites story status_transition <story-id> --skill
   satellites-technical-debt-review` — the client enacts the checkpoint edge into
   `techdebt-review` and runs that state's command against the LOCAL tree (exit
   code = pass/fail). A fail returns the story to `in_progress`; the 3rd fail
   escalates to `blocked`. A pass lands `done-review`.
6. From `done-review`, request `satellites story status_transition <story-id>
   --skill satellites-story-done-review`. The gate JUDGES; the client enacts
   pass → `done` / fail → `in_progress` (×3, then `blocked`).

A rejected gate returns notes; fix and request again — each reject is a real
transition back to `in_progress`, visible on the ledger. Only reviewers and the
client's deterministic enactment advance status — never hand-patch it. A story
in `blocked` is the operator's. When the ×N exhaustion was a recoverable FLAKE
you have fixed, request `satellites-loop-recovery-review` (blocked → in_progress)
with a `## Recovery` rationale; a genuine failure stays the operator's.

## States and actors

- `in_progress` (executor) — author the skill/principle, review it, publish it; every fail edge lands back here.
- `techdebt-review` (satellites) — advanced by the client running `satellites techdebt review`; exit code decides, no agent discretion.
- `done-review` (reviewer) — `satellites-story-done-review` judges the published artifact against the story; the client enacts its decision.
- `blocked` (operator) — fail-loop exhaustion lands here; the operator moves a story out, or the agent requests `satellites-loop-recovery-review` for a fixed flake.

## Checkpoint gates

This definition names every fail-closed check that runs while driving a
skills-repo story:

- [[satellites-technical-debt-review]] — the `techdebt-review` STATE's command,
  run via the status_transition traverse in step 5 against the local working
  tree BEFORE anything ships.

A traverse pass releases the [[satellites-commit-push]] capability, which
executes the remaining atomic gates pre-commit and honours their verdicts:

- [[satellites-workflow-drift-review]] — when the change touches process configuration (this workflow, gate or capability skills, principles).

```yaml
states:
  - backlog
  - ready
  - {name: in_progress,     actor: executor}
  - {name: techdebt-review, actor: satellites, command: "satellites techdebt review"}
  - {name: done-review,     actor: reviewer}
  - {name: blocked,         actor: operator}
  - done
  - cancelled
transitions:
  - {from: backlog,         to: ready,           reviewer_skill: "satellites-story-plan-review"}
  - {from: ready,           to: in_progress,     reviewer_skill: "satellites-story-start-review"}
  - {from: in_progress,     to: techdebt-review, trigger: checkpoint}
  - {from: techdebt-review, on: pass, to: done-review}
  - {from: techdebt-review, on: fail, to: in_progress, max_iterations: 3, on_exhausted: blocked}
  - {from: done-review,     on: pass, to: done, reviewer_skill: "satellites-story-done-review"}
  - {from: done-review,     on: fail, to: in_progress, max_iterations: 3, on_exhausted: blocked, reviewer_skill: "satellites-story-done-review"}
  - {from: blocked,         to: in_progress,     reviewer_skill: "satellites-loop-recovery-review"}
  - {from: backlog,         to: cancelled,       reviewer_skill: "satellites-story-cancel-review"}
  - {from: ready,           to: cancelled,       reviewer_skill: "satellites-story-cancel-review"}
  - {from: in_progress,     to: cancelled,       reviewer_skill: "satellites-story-cancel-review"}
```

## Environment

Drives a skills-repo story's document and the publisher repo working tree:
upserts the story body, requests gated transitions, triggers checkpoint
commits, and publishes the authored artifact to the library.

```yaml
guardrails:
  always:
    - Copy the Workflow yaml block into the story verbatim before requesting a gate.
    - Route every status change through the transition's reviewer skill or the client's deterministic enactment.
    - Author and review the artifact (the named capabilities) before publishing it.
    - Run the techdebt-review traverse to a pass before the checkpoint capability ships anything.
  ask_first:
    - Cancelling a story (the rationale is the operator's call unless already given).
  never:
    - Hand-patch story status or bypass a gate verdict.
    - Act in a state whose actor is not yours — blocked is the operator's.
    - Restate a named capability's routine inline — reference it by name and honour its verdict.
```
