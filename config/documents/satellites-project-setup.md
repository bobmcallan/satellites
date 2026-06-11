---
name: satellites-project-setup
type: skill
kind: capability
description: Define or revise a project's reviewer-gated workflow from the admin's requirements and the repo's reality. Invoke when a project has no workflow skill or wants to change how stories move; bakes in no other project's conventions.
scope: system
tags: [kind:capability, area:process]
---
# satellites-project-setup

Use this skill to define (or revise) a project's reviewer-gated workflow. Invoke it when a project has no workflow skill, or when an admin wants to change how stories move. You author the project-scoped skills from the admin's requirements and the repo's reality; this skill does not write them for you and bakes in no other project's conventions.

## Invariants you do not redefine

- **Only a reviewer advances a story's status.** The executing agent does the work and requests review. `satellites story review` runs each gate in a fresh reviewer-role context and enacts the transition.
- **The story is the contract.** At planning, instantiate the chosen workflow and the plan into the story body, so the reviewer reads one self-describing artifact.

One consequence any gate pair must respect: the completion gate **fails closed** — it rejects on any acceptance criterion it cannot confirm met. So every criterion must be satisfiable at its own story's completion. A criterion that depends on a different, not-yet-delivered story belongs to that enabler story; keep deferrals in prose, never in the criteria a gate checks. Author the plan gate to reject such a deferred criterion and the completion gate to fail closed on unmet ones.

## Steps

1. **Gather requirements.** Ask the admin what stages a unit of work passes through and what must be true to advance each. Read the repo to ground it — its build/test tooling, how changes ship, its review conventions.
2. **Define the workflow.** Write a `kind:workflow` skill whose `applies_to` lists the story types it drives. Its body states the states and ordered transitions, each naming the gate (`kind:gate` skill) that reviews it. Keep the first version minimal — one happy path.
3. **Define each gate.** For every transition, write a `kind:gate` skill stating, in the repo's own terms, the criteria a story must meet and how the reviewer verifies (read the body, run the repo's build/tests, check the named outcomes). A gate enacts the transition only on accept; on reject it records the reason and leaves status unchanged.
4. **Bind dispatch.** Give each skill the frontmatter the dispatch index reads — `name`, `kind`, `applies_to` (workflows), a `description` that says plainly when to use it.
5. **Publish.** Author the skills as project-scoped sources and `satellites skill upload` them; the content review runs on the way in.

## Anchor (parent) stories

For a story that only groups children (an epic/anchor), define a `parent` story type with a degenerate workflow: one state to a terminal state, gated by a close-review skill whose check is the children, not the code. That gate asserts the **genuine-anchor guard**: accept only when the anchor has at least one child AND every child is terminal; reject a childless or still-open anchor, naming the gap. The at-least-one-child clause is load-bearing — without it the gate passes vacuously over an empty set.

## Keep it small

Start with the fewest states and gates that make the work reviewable; add only when a real need appears.
