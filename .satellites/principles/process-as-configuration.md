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

## The boundary: gating authority vs rendering

"satellites holds no workflow knowledge" is a statement about **authority**,
not about every line of code. The line is:

- **Gating / transition authority — the skill owns it, the binary holds
  none.** The client never resolves a workflow, picks a transition, or
  computes a next status. `story status_transition --skill <gate>` runs the
  named gate; the gate reads the story's embedded `## Workflow` and enacts its
  own target. No workflow parsing on this path.
- **Rendering / observability — the binary MAY parse a workflow to display
  it.** The story-detail view and the process-trace overlay parse a workflow
  purely to *show* states, transitions, and where a story sits. This is
  read-only presentation; it decides nothing and advances nothing. Parsing for
  a picture is not holding authority.

So `internal/workflow` legitimately survives in the server for observability
(`story_detail`, `processtrace`) — what the epic retired is its use in the
*gating* path. When you touch a server site that parses a workflow, keep it on
the observability side of this line: render, never gate.

See [[agent-goals]], [[reviewer-process]].
