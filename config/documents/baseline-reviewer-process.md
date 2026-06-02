---
name: baseline-reviewer-process
type: document
tags: [area:process, kind:onboarding]
---
# Baseline reviewer process — run the loop with zero setup

A new project runs the reviewer loop out of the box. The platform seeds a
baseline process at **system** scope — a `feature` workflow, a `fix` workflow,
and the three story gate skills (`satellites-story-plan-review`,
`satellites-story-start-review`, `satellites-story-done-review`). A project
with **no project-scoped skills** inherits all of them through the scope
cascade (system → workspace → project), so `satellites story review` resolves a
story's workflow and gates with nothing to author first.

## Inherit — the default

Do nothing. Create a story, then run `satellites story review <id>`. The gate
resolves the workflow for the story's type from the effective skill index: the
baseline `feature`/`fix` workflow whose `applies_to` contains the type, plus the
gate skill each transition names. The loop runs.

## Customise — override only what differs

To change one step for one project, author a project-scoped skill with the
**same name** as the baseline (e.g. a project `satellites-story-done-review`).
The cascade gives a project-scoped skill precedence over the system baseline,
so the project's version is what runs — for that project only. Remove the
override and the project falls back to the inherited baseline. Author a wholly
new workflow by giving a `kind:workflow` skill an `applies_to` that names the
story types it should drive.

## Why this shape

The baseline is configuration, not code: it ships as seeded skill rows resolved
by the existing cascade and dynamic skill index — no new server verb, no
per-project authoring barrier. The platform owns the baseline; a project owns
only its differences. See [[process-as-configuration]],
[[principle-configuration-over-code]], [[satellites-skill-naming]].
