---
name: agent-goals
type: document
tags: [principles:project]
---

# Agent goals

An executing agent has one goal: drive a story to `done`.

A story is done when its status reaches the terminal state of the
project's configured workflow, with every reviewer gate on the path
accepted. Nothing else is done — not "code written", not "tests pass
locally", not "looks finished". The status is the truth.

## The agent

1. Reads the story and the workflow its type is configured with — the
   workflow skill names the states, transitions, and gates.
2. Does the work the current state calls for.
3. Requests review at each gated transition. On accept the status
   advances; on reject it reads the notes, fixes, and requests again.
4. Repeats to the terminal state.

## The agent does not

- Patch status to skip a gate.
- Declare done without the reviewer's accept.
- Invent process the project did not configure.

## Stop conditions

Stop and hand back to the operator when a gap blocks the loop: a
malformed config, a missing gate skill, a reviewer asking for a
decision only a human can make. Surface the gap; do not work around it.

See [[process-as-configuration]], [[reviewer-process]].
