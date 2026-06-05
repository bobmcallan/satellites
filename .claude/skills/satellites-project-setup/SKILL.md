<!-- satellites-sync:begin {"document_id":"doc_22e80e1c","version":3,"hash":"013333aff19b97559e78f62332665b1505e0c23962fc6fbc22d5aed9b5d1a02d"} satellites-sync:end -->
---
name: project-setup
type: skill
kind: capability
scope: system
tags: [kind:capability, area:process]
---
# project-setup

Use this skill to define (or revise) a project's reviewer-gated workflow. Invoke it when a project has no workflow skill yet, or when an admin wants to change how stories move. It is **guidance**: it teaches the structure and walks you through defining the process from the admin's requirements and the repo's reality. You author the project-scoped skills; this skill does not write them for you, and it bakes in no other project's conventions.

## What the platform fixes, and what you define

Two things are invariant and enforced in code — you do not redefine them:

- **Only a reviewer advances a story's status.** The executing agent does the work and *requests* review; it can never self-advance. `satellites story review` runs each gate in a fresh context under a reviewer-role key and enacts the transition.
- **The story is the contract.** At planning, instantiate the chosen workflow and the plan **into the story body**, so the reviewer reads one self-describing artifact. Plan-review validates the embedded workflow against its source once; later gates follow the workflow the story records.

Everything else is yours to define for the project: the states a story moves through, which transitions a reviewer gates, and **what each gate must verify** — expressed in the repo's own terms (its build and test commands, its review checklist, its story shape). Do not import another project's verification steps.

One structural consequence falls out of these invariants, and any gate pair you author must respect it. The completion gate verifies a story against its acceptance criteria and **fails closed** — it rejects on any criterion it cannot confirm met. So every acceptance criterion must be satisfiable at *its own story's* completion. A criterion whose satisfaction depends on a different, not-yet-delivered story is that enabler story's criterion, not this one's; duplicating it into this story's criteria guarantees a false reject the executor cannot honestly clear. Author the plan-stage gate to reject an acceptance-criteria field carrying such a deferred criterion, and the completion gate to fail closed on unmet ones — the two then agree, and an honestly-scoped story passes both. Deferrals belong in prose, never in the criteria a gate checks.

## Steps

1. **Gather requirements.** Ask the admin what stages a unit of work passes through and what must be true to advance each stage. Read the repo to ground it — its build/test tooling, how changes ship, its review conventions.
2. **Define the workflow.** Write a `kind:workflow` skill whose `applies_to` lists the story types it drives. Its body states the states and the ordered transitions, each naming the gate (`kind:gate` skill) that reviews it. Keep the first version minimal — one happy path.
3. **Define each gate.** For every transition, write a `kind:gate` skill stating, in the repo's own terms, the criteria a story must meet to pass — the executor's contract and the reviewer's checklist in one place. State how the reviewer verifies (read the body, run the repo's build/tests, check the named outcomes), and that a gate enacts the transition only on accept; on reject it records the reason and leaves status unchanged.
4. **Bind dispatch.** Give each skill the frontmatter the dispatch index reads — `name`, `kind`, `applies_to` (workflows), a `description` that says plainly when to use it.
5. **Publish.** Author the skills as project-scoped sources and upload them with the CLI (`satellites skill upload`); the content review runs on the way in. The loop then runs on the project's own process.

## Anchor (parent) stories

Some stories carry no executable work — an epic or anchor whose only job is to group children. Do not drive these through a build/test workflow. Define a `parent` story type (the story's `category`) with a degenerate workflow: one state to a terminal state, gated by a close-review skill whose check is the children, not the code.

That gate asserts the **genuine-anchor guard**: accept only when the anchor has at least one child AND every child has reached a terminal status; reject a childless or still-open anchor, naming the gap. The at-least-one-child clause is load-bearing — without it the gate passes vacuously over an empty set, and an executor could relabel any leaf story to the anchor type to win a free close. Closing stays reviewer-enacted like every transition; the anchor never patches its own status.

## Keep it small

Start with the fewest states and gates that make the work reviewable, and add only when a real need appears. A workflow the admin can read in one screen is the goal.
