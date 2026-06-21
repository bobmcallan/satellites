---
name: work-artifact-selection
type: document
scope: system
tags: [principles:global, principles:always]
---

# Work-artifact selection

Not all work is a story — reaching for one by default is the common mistake.
When asked to create work (a "task", a scan, a report, a capability), choose the
primitive that fits its shape first:

- **Story** — one-off work driven through a workflow to `done` once.
- **Skill** — a reusable CAPABILITY: the how-to the agent runs repeatedly (e.g. a
  scan that emits a report). A recurring or per-run/dated output is a skill, not a
  story.
- **Task** — a governed, RE-RUNNABLE RUNNER: a repeatable job, run on demand or on
  a schedule, that records each run. It needs only a clear work statement and a
  resolvable task workflow; backing its work step with a skill is OPTIONAL —
  extract one when the capability is reused.

The signal is recurrence: work that runs again, or whose output is dated or
versioned per run, is a skill or a task; a single deliverable is a story. Skill
and task COMPOSE — the skill is the capability, the task is the governed runner
that invokes it.

**Choosing a primitive means building it that way — and that is the agent's job,
not the user's.** Never ask a human to pick the primitive or settle its
architecture; structure it from this principle. When the chosen primitive has no
governed path — recurring work but no task workflow, so it would fall into a
one-shot story/deploy workflow — that is a CONFIG GAP to surface (name the missing
workflow), not a question to put to the user.

For the artifact-KIND test (skill vs principle vs document) see [[substrate-taxonomy]].
