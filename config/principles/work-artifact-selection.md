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

**One clarification is allowed — recurrence, never the primitive.** Whether work
will RECUR is a user fact you cannot derive, and the primitive follows from it. So
when the recurrence signal is genuinely ambiguous:

- Elicit the requirement once, at intake, while intent is freshest: *"a one-off, or will you re-run it and want each run recorded?"*
- Choose the primitive from the answer YOURSELF — never ask "task or story?".
- Don't lean on the word "task": it is both an everyday noun and a primitive name, so it is a weak signal — weigh recurrence and per-run/dated output instead.

**A task IS its work definition.** A task's body states the action, the output,
and how success is verified — that is the work, judged by the same reviewer model
as a story (the agent does the work; reviewers gate it). A task MAY name a skill
it wants the agent to use, but that skill is a Claude capability in the agent's
environment, not satellites substrate to resolve or govern — at most a warning to
the executing agent.

For the artifact-KIND test (skill vs principle vs document) see [[substrate-taxonomy]].
