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
- **Skill** — a reusable capability the agent runs repeatedly (e.g. a scan that
  emits a report). A recurring or per-run/dated output is a skill, not a story.
- **Task** — a repeatable job, run on demand or on a schedule.

The signal is recurrence: work that runs again, or whose output is dated or
versioned per run, is a skill or a task; a single deliverable is a story. For
the artifact-KIND test (skill vs principle vs document) see [[substrate-taxonomy]].
