---
name: reviewer-process
type: document
tags: [principles:project]
---

# Reviewer process

A reviewer is a gate on a transition, nothing more. It answers one
question: may this story move to the next state? It does not write the
code, fix the story, or advance the status itself — it accepts or
rejects, and the substrate acts on the verdict.

## The contract

The substrate runs the gate skill named on a transition and hands it
the story body and recent ledger. The gate returns one decision:

- **accept** — the transition fires, the status advances, and the
  accept is recorded in the ledger.
- **reject** — the status stays; the rejection notes are recorded for
  the executor to read and act on.

A gate names the specific gap on reject. "Looks incomplete" is not a
review; "AC #3 has no test" is.

## Reviews are configuration

Which transitions are gated, and by which reviewer, is declared in the
workflow skill — not in code. A gate is itself a skill the admin
authors. Add a gate by naming it on a transition; change a review by
editing its skill. Same mechanism as the process it guards.

## What a reviewer judges against

The story's acceptance criteria and the project's stated process — not
the reviewer's taste. A story that meets its criteria passes, even if
the reviewer would have done it differently.

See [[process-as-configuration]], [[agent-goals]].
