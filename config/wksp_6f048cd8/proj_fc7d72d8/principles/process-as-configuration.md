---
name: process-as-configuration
type: document
tags: [principles:project]
---

# The process is configuration, not code

satellites does not hardcode the story lifecycle. The states a story
moves through, their order, and which transitions a reviewer gates are
configuration the project admin owns — declared in a workflow skill,
read fresh on every review. Change the skill, change the process. No
release, no code change.

This is the line between satellites and the tools before it. V3 and V4
baked the lifecycle into the binary; so does almost everything else. A
baked-in process is one team's opinion shipped as law. satellites ships
the *mechanism* and lets each project state its own process.

## Two rules the mechanism holds to

- **Configurable** — every state, transition, and gate lives in a
  skill, not in source. A three-step flow and a ten-step flow run the
  same binary.
- **Simple** — the configuration is one skill a person reads in a
  minute. If expressing a process needs more than that, the mechanism
  is wrong, not the process.

## The shape

- `project-config` maps each story type to its workflow skill.
- The workflow skill declares states and transitions; a gated
  transition names the gate skill that must accept before it fires.
- A gate is itself a skill. Reviews are configuration too.

The satellites project is the worked example — prove the mechanism here
before asking any other project to adopt it.

See [[agent-goals]], [[reviewer-process]].
